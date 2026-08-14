// Package mockbackend provides a small in-memory ANP backend used by tests and
// local smoke runs. It verifies HTTP Message Signatures with the SDK and
// implements the ANP standard JSON-RPC methods: direct messaging (plaintext
// base + E2EE) and group messaging (anp.group.base.v1).
package mockbackend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	anpauth "github.com/agent-network-protocol/anp/golang/authentication"
)

// Message is a stored direct or group envelope.
type Message struct {
	MessageID string         `json:"message_id,omitempty"`
	Secure    bool           `json:"secure,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
	Body      map[string]any `json:"body,omitempty"`
}

// groupRecord is the mock's in-memory group state.
type groupRecord struct {
	GroupDID     string
	Profile      map[string]any
	Policy       map[string]any
	Members      []map[string]any // group_member objects
	StateVersion int
	EventSeq     int
	CreatorDID   string
}

// objectRecord is the mock's in-memory attachment object state (P7 data plane).
type objectRecord struct {
	AttachmentID string
	SlotID       string
	UploadURI    string
	ObjectURI    string
	CommitToken  string
	MimeType     string
	Filename     string
	Committed    bool
	Bytes        []byte
}

// ticketRecord is an issued download ticket (P7 data plane).
type ticketRecord struct {
	Ticket       string
	AttachmentID string
}

// maxBodyBytes caps the in-memory size of an inbound JSON-RPC body.
const maxBodyBytes = 1 << 20 // 1 MiB

// Server is an in-memory ANP backend.
type Server struct {
	mu             sync.Mutex
	messages       []Message
	didDocs        map[string]map[string]any // sender DID -> document (for signature verification)
	handles        map[string]string         // handle -> did
	prekeyBundles  map[string]map[string]any // owner DID -> prekey bundle
	oneTimePrekeys map[string][]map[string]any
	groups         map[string]*groupRecord  // group DID -> group state
	objects        map[string]*objectRecord // attachment_id -> object state
	slots          map[string]*objectRecord // slot_id -> object state
	tickets        map[string]*ticketRecord // ticket -> ticket state
	baseURL        string                   // set by Start
	nextMessage    int64
	nextGroup      int64
}

func New() *Server {
	return &Server{
		didDocs:        map[string]map[string]any{},
		handles:        map[string]string{},
		prekeyBundles:  map[string]map[string]any{},
		oneTimePrekeys: map[string][]map[string]any{},
		groups:         map[string]*groupRecord{},
		objects:        map[string]*objectRecord{},
		slots:          map[string]*objectRecord{},
		tickets:        map[string]*ticketRecord{},
	}
}

// AddIdentity registers a DID document so the mock can verify its signatures.
func (s *Server) AddIdentity(did string, doc map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.didDocs[did] = doc
}

// Handler returns the /rpc HTTP handler plus the P7 data-plane routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		s.handleRPC(w, r)
	})
	mux.HandleFunc("/objects/upload/", s.handleUpload)
	mux.HandleFunc("/objects/", s.handleDownload)
	return mux
}

// Start launches the mock backend on a random local port.
func (s *Server) Start() (baseURL string, closeFn func(), err error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	server := &http.Server{Handler: s.Handler()}
	go func() {
		_ = server.Serve(listener)
	}()
	baseURL = "http://" + listener.Addr().String()
	s.mu.Lock()
	s.baseURL = baseURL
	s.mu.Unlock()
	return baseURL, func() { _ = server.Shutdown(context.Background()) }, nil
}

// Messages returns a copy of the stored messages.
func (s *Server) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.messages...)
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	// Bound the read: ignore the client-supplied Content-Length (which may be
	// -1 for chunked bodies) and cap at maxBodyBytes to avoid a huge or
	// negative allocation.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		writeJSON(w, map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": -32700, "message": "read error"}, "id": nil})
		return
	}
	if len(body) > maxBodyBytes {
		writeJSON(w, map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": -32600, "message": "request too large"}, "id": nil})
		return
	}

	headers := map[string]string{}
	for key, values := range r.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	var request struct {
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
		ID     any            `json:"id"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		writeJSON(w, map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": -32700, "message": "parse error"}, "id": nil})
		return
	}
	if request.Method != "did.register_document" {
		if err := s.verifySignature(r, headers, body); err != nil {
			writeJSON(w, map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": -32601, "message": err.Error()}, "id": request.ID})
			return
		}
	}
	result, err := s.dispatch(request.Method, request.Params)
	response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
	if err != nil {
		response["error"] = map[string]any{"code": -32000, "message": err.Error()}
	} else {
		response["result"] = result
	}
	writeJSON(w, response)
}

// verifySignature checks the HTTP Message Signature when identities are
// registered. With no registered identities it accepts any request.
func (s *Server) verifySignature(r *http.Request, headers map[string]string, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	signatureInput := getHeader(headers, "Signature-Input")
	if signatureInput == "" {
		return nil
	}
	if len(s.didDocs) == 0 {
		return nil
	}
	requestURL := "http://" + r.Host + r.URL.Path
	for _, doc := range s.didDocs {
		if _, err := anpauth.VerifyHTTPMessageSignature(doc, http.MethodPost, requestURL, headers, body); err == nil {
			return nil
		}
	}
	return fmt.Errorf("signature verification failed")
}

func (s *Server) dispatch(method string, params map[string]any) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch method {
	case "msg.inbox":
		return s.inbox(params)
	case "did.resolve":
		return s.resolveDID(params)
	case "did.register_document":
		return s.registerDocument(params)
	case "handle.register":
		return s.registerHandle(params)
	case "handle.recover":
		return map[string]any{"status": "recovered"}, nil
	case "direct.send":
		return s.directSend(params)
	case "direct.e2ee.publish_prekey_bundle":
		return s.publishPrekeyBundle(params)
	case "direct.e2ee.get_prekey_bundle":
		return s.getPrekeyBundle(params)
	case "group.create":
		return s.groupCreate(params)
	case "group.get_info":
		return s.groupGetInfo(params)
	case "group.join":
		return s.groupJoin(params)
	case "group.add":
		return s.groupAdd(params)
	case "group.remove":
		return s.groupRemove(params)
	case "group.leave":
		return s.groupLeave(params)
	case "group.update_profile":
		return s.groupUpdateProfile(params)
	case "group.update_policy":
		return s.groupUpdatePolicy(params)
	case "group.send":
		return s.groupSend(params)
	case "attachment.create_slot":
		return s.attachmentCreateSlot(params)
	case "attachment.commit_object":
		return s.attachmentCommitObject(params)
	case "attachment.abort_object":
		return s.attachmentAbortObject(params)
	case "attachment.get_download_ticket":
		return s.attachmentGetDownloadTicket(params)
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

func (s *Server) registerDocument(params map[string]any) (any, error) {
	did, _ := params["did"].(string)
	doc, _ := params["did_document"].(map[string]any)
	if did == "" || doc == nil {
		return nil, fmt.Errorf("did and did_document are required")
	}
	s.didDocs[did] = doc
	return map[string]any{"did": did, "status": "registered"}, nil
}

// directSend stores a direct envelope for the recipient's inbox and returns
// the standard acknowledgment. Envelopes with profile anp.direct.e2ee.v1 are
// marked secure; anp.direct.base.v1 envelopes are transport-protected
// plaintext.
func (s *Server) directSend(params map[string]any) (any, error) {
	meta, _ := params["meta"].(map[string]any)
	body, _ := params["body"].(map[string]any)
	target, _ := meta["target"].(map[string]any)
	peerDID, _ := target["did"].(string)
	messageID, _ := meta["message_id"].(string)
	operationID, _ := meta["operation_id"].(string)
	profile, _ := meta["profile"].(string)
	if messageID == "" {
		return nil, fmt.Errorf("message_id is required")
	}
	secure := profile != "" && profile != "anp.direct.base.v1"
	s.nextMessage++
	s.messages = append(s.messages, Message{
		MessageID: messageID,
		Secure:    secure,
		Meta:      meta,
		Body:      body,
	})
	return map[string]any{
		"accepted":     true,
		"message_id":   messageID,
		"operation_id": operationID,
		"target_did":   peerDID,
		"accepted_at":  time.Now().UTC().Format(time.RFC3339),
		"body":         body,
	}, nil
}

// groupCreate implements group.create: registers a new group and makes the
// sender its owner.
func (s *Server) groupCreate(params map[string]any) (any, error) {
	meta, _ := params["meta"].(map[string]any)
	body, _ := params["body"].(map[string]any)
	senderDID, _ := meta["sender_did"].(string)
	profile, _ := body["group_profile"].(map[string]any)
	policy, _ := body["group_policy"].(map[string]any)
	if profile == nil {
		profile = map[string]any{}
	}
	if policy == nil {
		return nil, fmt.Errorf("group_policy is required")
	}
	s.nextGroup++
	groupDID := fmt.Sprintf("did:wba:groups.example:group-%d", s.nextGroup)
	rec := &groupRecord{
		GroupDID:     groupDID,
		Profile:      profile,
		Policy:       policy,
		Members:      []map[string]any{{"agent_did": senderDID, "role": "owner", "status": "active"}},
		StateVersion: 1,
		CreatorDID:   senderDID,
	}
	s.groups[groupDID] = rec
	return map[string]any{
		"group_did":           groupDID,
		"group_state_version": rec.StateVersion,
		"created_at":          time.Now().UTC().Format(time.RFC3339),
		"creator_did":         senderDID,
		"group_event_seq":     "0",
		"group_profile":       profile,
		"group_policy":        policy,
	}, nil
}

func (s *Server) groupGetInfo(params map[string]any) (any, error) {
	meta, _ := params["meta"].(map[string]any)
	body, _ := params["body"].(map[string]any)
	target, _ := meta["target"].(map[string]any)
	groupDID, _ := target["did"].(string)
	rec, ok := s.groups[groupDID]
	if !ok {
		return nil, fmt.Errorf("group.not_found")
	}
	result := map[string]any{
		"group_did":           rec.GroupDID,
		"group_state_version": rec.StateVersion,
		"group_profile":       rec.Profile,
	}
	if include, _ := body["include_policy"].(bool); include {
		result["group_policy"] = rec.Policy
	}
	if include, _ := body["include_member_list"].(bool); include {
		result["member_list"] = rec.Members
	}
	result["member_count"] = fmt.Sprintf("%d", len(activeMembers(rec.Members)))
	return result, nil
}

func (s *Server) groupJoin(params map[string]any) (any, error) {
	meta, _ := params["meta"].(map[string]any)
	senderDID, _ := meta["sender_did"].(string)
	groupDID := s.groupTargetDID(meta)
	rec, ok := s.groups[groupDID]
	if !ok {
		return nil, fmt.Errorf("group.not_found")
	}
	s.upsertMember(rec, senderDID, "member")
	rec.StateVersion++
	return map[string]any{
		"group_did":           rec.GroupDID,
		"member_did":          senderDID,
		"membership_status":   "active",
		"group_state_version": rec.StateVersion,
	}, nil
}

func (s *Server) groupAdd(params map[string]any) (any, error) {
	meta, _ := params["meta"].(map[string]any)
	body, _ := params["body"].(map[string]any)
	groupDID := s.groupTargetDID(meta)
	rec, ok := s.groups[groupDID]
	if !ok {
		return nil, fmt.Errorf("group.not_found")
	}
	memberDID, _ := body["member_did"].(string)
	if memberDID == "" {
		handle, _ := body["member_handle"].(string)
		if did, ok := s.handles[handle]; ok {
			memberDID = did
		} else {
			return nil, fmt.Errorf("member handle not found")
		}
	}
	role, _ := body["role"].(string)
	if role == "" {
		role = "member"
	}
	s.upsertMember(rec, memberDID, role)
	rec.StateVersion++
	return map[string]any{
		"group_did":           rec.GroupDID,
		"member_did":          memberDID,
		"membership_status":   "active",
		"group_state_version": rec.StateVersion,
	}, nil
}

func (s *Server) groupRemove(params map[string]any) (any, error) {
	meta, _ := params["meta"].(map[string]any)
	body, _ := params["body"].(map[string]any)
	groupDID := s.groupTargetDID(meta)
	rec, ok := s.groups[groupDID]
	if !ok {
		return nil, fmt.Errorf("group.not_found")
	}
	memberDID, _ := body["member_did"].(string)
	s.removeMember(rec, memberDID)
	rec.StateVersion++
	return map[string]any{
		"group_did":           rec.GroupDID,
		"member_did":          memberDID,
		"group_state_version": rec.StateVersion,
	}, nil
}

func (s *Server) groupLeave(params map[string]any) (any, error) {
	meta, _ := params["meta"].(map[string]any)
	senderDID, _ := meta["sender_did"].(string)
	groupDID := s.groupTargetDID(meta)
	rec, ok := s.groups[groupDID]
	if !ok {
		return nil, fmt.Errorf("group.not_found")
	}
	s.removeMember(rec, senderDID)
	rec.StateVersion++
	return map[string]any{
		"group_did":           rec.GroupDID,
		"leaver_did":          senderDID,
		"group_state_version": rec.StateVersion,
	}, nil
}

func (s *Server) groupUpdateProfile(params map[string]any) (any, error) {
	meta, _ := params["meta"].(map[string]any)
	body, _ := params["body"].(map[string]any)
	groupDID := s.groupTargetDID(meta)
	rec, ok := s.groups[groupDID]
	if !ok {
		return nil, fmt.Errorf("group.not_found")
	}
	patch, _ := body["group_profile_patch"].(map[string]any)
	mergePatch(rec.Profile, patch)
	rec.StateVersion++
	return map[string]any{
		"group_did":           rec.GroupDID,
		"group_state_version": rec.StateVersion,
		"group_profile":       rec.Profile,
	}, nil
}

func (s *Server) groupUpdatePolicy(params map[string]any) (any, error) {
	meta, _ := params["meta"].(map[string]any)
	body, _ := params["body"].(map[string]any)
	groupDID := s.groupTargetDID(meta)
	rec, ok := s.groups[groupDID]
	if !ok {
		return nil, fmt.Errorf("group.not_found")
	}
	patch, _ := body["group_policy_patch"].(map[string]any)
	mergePatch(rec.Policy, patch)
	rec.StateVersion++
	return map[string]any{
		"group_did":           rec.GroupDID,
		"group_state_version": rec.StateVersion,
		"group_policy":        rec.Policy,
	}, nil
}

func (s *Server) groupSend(params map[string]any) (any, error) {
	meta, _ := params["meta"].(map[string]any)
	body, _ := params["body"].(map[string]any)
	groupDID := s.groupTargetDID(meta)
	rec, ok := s.groups[groupDID]
	if !ok {
		return nil, fmt.Errorf("group.not_found")
	}
	messageID, _ := meta["message_id"].(string)
	operationID, _ := meta["operation_id"].(string)
	if messageID == "" {
		return nil, fmt.Errorf("message_id is required")
	}
	rec.EventSeq++
	s.nextMessage++
	s.messages = append(s.messages, Message{
		MessageID: messageID,
		Secure:    false,
		Meta:      meta,
		Body:      body,
	})
	return map[string]any{
		"accepted":            true,
		"group_did":           rec.GroupDID,
		"message_id":          messageID,
		"operation_id":        operationID,
		"group_event_seq":     fmt.Sprintf("%d", rec.EventSeq),
		"group_state_version": rec.StateVersion,
		"accepted_at":         time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (s *Server) groupTargetDID(meta map[string]any) string {
	target, _ := meta["target"].(map[string]any)
	did, _ := target["did"].(string)
	return did
}

// ---------------------------------------------------------------- attachment (P7)

func (s *Server) attachmentCreateSlot(params map[string]any) (any, error) {
	body, _ := params["body"].(map[string]any)
	attachmentID, _ := body["attachment_id"].(string)
	if attachmentID == "" {
		return nil, fmt.Errorf("attachment_id is required")
	}
	if _, exists := s.objects[attachmentID]; exists {
		return nil, fmt.Errorf("anp.attachment.slot_already_exists")
	}
	slotID := fmt.Sprintf("slot_%d", time.Now().UnixNano())
	record := &objectRecord{
		AttachmentID: attachmentID,
		SlotID:       slotID,
		UploadURI:    s.baseURL + "/objects/upload/" + slotID,
		ObjectURI:    s.baseURL + "/objects/" + attachmentID,
		CommitToken:  fmt.Sprintf("tok_%d", time.Now().UnixNano()),
		MimeType:     stringField(body, "mime_type"),
		Filename:     stringField(body, "filename"),
	}
	s.objects[attachmentID] = record
	s.slots[slotID] = record
	return map[string]any{
		"attachment_id": attachmentID,
		"slot_id":       slotID,
		"upload_uri":    record.UploadURI,
		"object_uri":    record.ObjectURI,
		"commit_token":  record.CommitToken,
		"expires_at":    time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
	}, nil
}

func (s *Server) attachmentCommitObject(params map[string]any) (any, error) {
	body, _ := params["body"].(map[string]any)
	attachmentID, _ := body["attachment_id"].(string)
	slotID, _ := body["slot_id"].(string)
	commitToken, _ := body["commit_token"].(string)
	record, ok := s.objects[attachmentID]
	if !ok {
		return nil, fmt.Errorf("anp.attachment.slot_not_found")
	}
	if record.SlotID != slotID {
		return nil, fmt.Errorf("anp.attachment.slot_not_found")
	}
	if record.CommitToken != commitToken {
		return nil, fmt.Errorf("anp.attachment.commit_token_invalid")
	}
	record.Committed = true
	return map[string]any{
		"committed":     true,
		"attachment_id": attachmentID,
		"object_uri":    record.ObjectURI,
		"committed_at":  time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (s *Server) attachmentAbortObject(params map[string]any) (any, error) {
	body, _ := params["body"].(map[string]any)
	attachmentID, _ := body["attachment_id"].(string)
	record, ok := s.objects[attachmentID]
	if !ok {
		return nil, fmt.Errorf("anp.attachment.slot_not_found")
	}
	delete(s.objects, attachmentID)
	delete(s.slots, record.SlotID)
	return map[string]any{
		"aborted":       true,
		"attachment_id": attachmentID,
		"aborted_at":    time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (s *Server) attachmentGetDownloadTicket(params map[string]any) (any, error) {
	body, _ := params["body"].(map[string]any)
	attachmentID, _ := body["attachment_id"].(string)
	record, ok := s.objects[attachmentID]
	if !ok || !record.Committed {
		return nil, fmt.Errorf("anp.attachment.grant_not_found")
	}
	ticket := fmt.Sprintf("tkt_%d", time.Now().UnixNano())
	s.tickets[ticket] = &ticketRecord{Ticket: ticket, AttachmentID: attachmentID}
	return map[string]any{
		"download_ticket_b64u": ticket,
		"expires_at":           time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
		"ticket_binding": map[string]any{
			"attachment_id": attachmentID,
			"object_uri":    record.ObjectURI,
		},
	}, nil
}

// handleUpload stores object bytes at the data-plane upload URI (PUT).
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	slotID := strings.TrimPrefix(r.URL.Path, "/objects/upload/")
	s.mu.Lock()
	record, ok := s.slots[slotID]
	s.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil || len(body) > maxBodyBytes {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	record.Bytes = body
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

// handleDownload serves committed object bytes with a valid bearer ticket.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	attachmentID := strings.TrimPrefix(r.URL.Path, "/objects/")
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ticket := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
	s.mu.Lock()
	record, ok := s.objects[attachmentID]
	ticketRec, ticketOK := s.tickets[ticket]
	s.mu.Unlock()
	if !ok || !record.Committed || !ticketOK || ticketRec.AttachmentID != attachmentID {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	bytes := append([]byte(nil), record.Bytes...)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(bytes)
}

func stringField(body map[string]any, key string) string {
	value, _ := body[key].(string)
	return value
}

func (s *Server) upsertMember(rec *groupRecord, did, role string) {
	for i, member := range rec.Members {
		if member["agent_did"] == did {
			rec.Members[i]["role"] = role
			rec.Members[i]["status"] = "active"
			return
		}
	}
	rec.Members = append(rec.Members, map[string]any{"agent_did": did, "role": role, "status": "active"})
}

func (s *Server) removeMember(rec *groupRecord, did string) {
	for i, member := range rec.Members {
		if member["agent_did"] == did {
			rec.Members[i]["status"] = "removed"
			return
		}
	}
}

func activeMembers(members []map[string]any) []map[string]any {
	var active []map[string]any
	for _, member := range members {
		if member["status"] == "active" {
			active = append(active, member)
		}
	}
	return active
}

// mergePatch applies a shallow RFC 7386-style merge of patch into target.
func mergePatch(target map[string]any, patch map[string]any) {
	for key, value := range patch {
		if value == nil {
			delete(target, key)
			continue
		}
		target[key] = value
	}
}

func (s *Server) publishPrekeyBundle(params map[string]any) (any, error) {
	body, _ := params["body"].(map[string]any)
	bundle, _ := body["prekey_bundle"].(map[string]any)
	ownerDID, _ := bundle["owner_did"].(string)
	bundleID, _ := bundle["bundle_id"].(string)
	if ownerDID == "" {
		return nil, fmt.Errorf("prekey_bundle.owner_did is required")
	}
	s.prekeyBundles[ownerDID] = bundle
	if raw, ok := body["one_time_prekeys"].([]any); ok {
		list := []map[string]any{}
		for _, entry := range raw {
			if item, ok := entry.(map[string]any); ok {
				list = append(list, item)
			}
		}
		s.oneTimePrekeys[ownerDID] = append(s.oneTimePrekeys[ownerDID], list...)
	}
	return map[string]any{
		"published":    true,
		"owner_did":    ownerDID,
		"bundle_id":    bundleID,
		"published_at": time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (s *Server) getPrekeyBundle(params map[string]any) (any, error) {
	body, _ := params["body"].(map[string]any)
	targetDID, _ := body["target_did"].(string)
	requireOPK, _ := body["require_opk"].(bool)
	bundle, ok := s.prekeyBundles[targetDID]
	if !ok {
		return nil, fmt.Errorf("anp.direct.e2ee.bundle_not_found")
	}
	result := map[string]any{"target_did": targetDID, "prekey_bundle": bundle}
	if requireOPK {
		queue := s.oneTimePrekeys[targetDID]
		if len(queue) > 0 {
			result["one_time_prekey"] = queue[0]
			s.oneTimePrekeys[targetDID] = queue[1:]
		} else {
			return nil, fmt.Errorf("anp.direct.e2ee.opk_unavailable")
		}
	}
	return result, nil
}

// inbox returns stored messages as standard {server_seq, meta, body} envelopes.
func (s *Server) inbox(params map[string]any) (any, error) {
	limit := 100
	if value, ok := params["limit"].(float64); ok {
		// Guard the float->int conversion: NaN/Inf/out-of-range floats would
		// otherwise produce implementation-dependent garbage.
		if value >= 1 && value <= 1000 && value == value && value <= 1<<53 {
			limit = int(value)
		}
	}
	rows := []map[string]any{}
	for index, message := range s.messages {
		rows = append(rows, map[string]any{
			"server_seq": index + 1,
			"meta":       message.Meta,
			"body":       message.Body,
		})
		if len(rows) >= limit {
			break
		}
	}
	return map[string]any{"messages": rows}, nil
}

func (s *Server) resolveDID(params map[string]any) (any, error) {
	target, _ := params["target"].(string)
	if strings.HasPrefix(target, "did:") {
		if doc, ok := s.didDocs[target]; ok {
			return map[string]any{"did": target, "did_document": doc}, nil
		}
		return nil, fmt.Errorf("did not found")
	}
	if did, ok := s.handles[target]; ok {
		if doc, ok := s.didDocs[did]; ok {
			return map[string]any{"did": did, "did_document": doc}, nil
		}
	}
	return nil, fmt.Errorf("handle not found")
}

func (s *Server) registerHandle(params map[string]any) (any, error) {
	handle, _ := params["handle"].(string)
	did, _ := params["did"].(string)
	if handle == "" || did == "" {
		return nil, fmt.Errorf("handle and did are required")
	}
	// Simulate squatting: a handle bound to a different DID is taken.
	if existing, ok := s.handles[handle]; ok && existing != did {
		return nil, fmt.Errorf("handle %q is already registered by another identity", handle)
	}
	s.handles[handle] = did
	return map[string]any{"did": did, "handle": handle, "status": "registered"}, nil
}

func getHeader(headers map[string]string, key string) string {
	for candidate, value := range headers {
		if strings.EqualFold(candidate, key) {
			return value
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
