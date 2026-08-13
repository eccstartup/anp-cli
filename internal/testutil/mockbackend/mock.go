// Package mockbackend provides a small in-memory ANP backend used by tests and
// local smoke runs. It verifies HTTP Message Signatures with the SDK and
// implements the documented JSON-RPC methods.
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

type Message struct {
	MessageID    string `json:"message_id"`
	SenderDID    string `json:"sender_did"`
	RecipientDID string `json:"recipient_did,omitempty"`
	GroupDID     string `json:"group_did,omitempty"`
	Type         string `json:"type,omitempty"`
	Text         string `json:"text,omitempty"`
	Secure       bool   `json:"secure,omitempty"`
	SentAt       string `json:"sent_at,omitempty"`
	Meta         any    `json:"meta,omitempty"`
	Body         any    `json:"body,omitempty"`
}

type Group struct {
	GroupDID string   `json:"group_did"`
	Name     string   `json:"name"`
	OwnerDID string   `json:"owner_did"`
	Members  []string `json:"members"`
}

// maxBodyBytes caps the in-memory size of an inbound JSON-RPC body.
const maxBodyBytes = 1 << 20 // 1 MiB

// Server is an in-memory ANP backend.
type Server struct {
	mu             sync.Mutex
	messages       []Message
	groups         map[string]*Group
	didDocs        map[string]map[string]any   // sender DID -> document (for signature verification)
	handles        map[string]string           // handle -> did
	prekeyBundles  map[string]map[string]any   // owner DID -> prekey bundle
	oneTimePrekeys map[string][]map[string]any // owner DID -> queued OPKs
	nextMessage    int
	nextGroup      int
}

func New() *Server {
	return &Server{
		groups:         map[string]*Group{},
		didDocs:        map[string]map[string]any{},
		handles:        map[string]string{},
		prekeyBundles:  map[string]map[string]any{},
		oneTimePrekeys: map[string][]map[string]any{},
	}
}

// AddIdentity registers a DID document so the mock can verify its signatures.
func (s *Server) AddIdentity(did string, doc map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.didDocs[did] = doc
}

// Handler returns the /rpc HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		s.handleRPC(w, r)
	})
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
	case "msg.send":
		return s.send(params)
	case "msg.inbox":
		return s.inbox(params)
	case "msg.history":
		return s.history(params)
	case "group.create":
		return s.createGroup(params)
	case "group.join":
		return s.joinGroup(params)
	case "group.leave":
		return s.leaveGroup(params)
	case "group.members":
		return s.groupMembers(params)
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

// directSend stores a direct E2EE envelope for the recipient's inbox.
func (s *Server) directSend(params map[string]any) (any, error) {
	meta, _ := params["meta"].(map[string]any)
	body, _ := params["body"].(map[string]any)
	target, _ := meta["target"].(map[string]any)
	peerDID, _ := target["did"].(string)
	messageID, _ := meta["message_id"].(string)
	senderDID, _ := meta["sender_did"].(string)
	if messageID == "" {
		return nil, fmt.Errorf("message_id is required")
	}
	s.nextMessage++
	s.messages = append(s.messages, Message{
		MessageID: messageID,
		SenderDID: senderDID,
		Secure:    true,
		SentAt:    time.Now().UTC().Format(time.RFC3339),
		Meta:      meta,
		Body:      body,
	})
	_ = peerDID
	return map[string]any{"message_id": messageID, "state": "delivered", "sent_at": time.Now().UTC().Format(time.RFC3339)}, nil
}

func (s *Server) publishPrekeyBundle(params map[string]any) (any, error) {
	meta, _ := params["meta"].(map[string]any)
	body, _ := params["body"].(map[string]any)
	bundle, _ := body["prekey_bundle"].(map[string]any)
	ownerDID, _ := bundle["owner_did"].(string)
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
	_ = meta
	return map[string]any{"status": "published", "owner_did": ownerDID}, nil
}

func (s *Server) getPrekeyBundle(params map[string]any) (any, error) {
	body, _ := params["body"].(map[string]any)
	targetDID, _ := body["target_did"].(string)
	requireOPK, _ := body["require_opk"].(bool)
	bundle, ok := s.prekeyBundles[targetDID]
	if !ok {
		return nil, fmt.Errorf("prekey bundle not found for %s", targetDID)
	}
	result := map[string]any{"prekey_bundle": bundle}
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

func (s *Server) send(params map[string]any) (any, error) {
	to, _ := params["to"].(string)
	group, _ := params["group"].(string)
	body, _ := params["body"].(map[string]any)
	secure, _ := params["secure"].(bool)
	text := ""
	if body != nil {
		if value, ok := body["text"].(string); ok {
			text = value
		}
	}
	if to == "" && group == "" {
		return nil, fmt.Errorf("either to or group is required")
	}
	s.nextMessage++
	message := Message{
		MessageID: fmt.Sprintf("msg_%d", s.nextMessage),
		SenderDID: "", // resolved by caller context; set below
		Type:      "text",
		Text:      text,
		Secure:    secure,
		SentAt:    time.Now().UTC().Format(time.RFC3339),
	}
	s.messages = append(s.messages, message)
	return map[string]any{
		"message_id": message.MessageID,
		"thread_id":  "thread_" + firstNonEmpty(to, group),
		"sent_at":    message.SentAt,
		"state":      "delivered",
	}, nil
}

func (s *Server) inbox(params map[string]any) (any, error) {
	scope, _ := params["scope"].(string)
	limit := 100
	if value, ok := params["limit"].(float64); ok {
		// Guard the float->int conversion: NaN/Inf/out-of-range floats would
		// otherwise produce implementation-dependent garbage.
		if value >= 1 && value <= 1000 && value == value && value <= 1<<53 {
			limit = int(value)
		}
	}
	rows := []Message{}
	for _, message := range s.messages {
		if scope == "direct" && message.GroupDID != "" {
			continue
		}
		if scope == "group" && message.GroupDID == "" {
			continue
		}
		rows = append(rows, message)
		if len(rows) >= limit {
			break
		}
	}
	return map[string]any{"messages": rows}, nil
}

func (s *Server) history(params map[string]any) (any, error) {
	rows := []Message{}
	_ = params
	rows = append(rows, s.messages...)
	return map[string]any{"messages": rows}, nil
}

func (s *Server) createGroup(params map[string]any) (any, error) {
	name, _ := params["name"].(string)
	s.nextGroup++
	groupDID := fmt.Sprintf("did:wba:mock:group:g%d", s.nextGroup)
	group := &Group{GroupDID: groupDID, Name: name, Members: []string{}}
	s.groups[groupDID] = group
	return map[string]any{"group_did": groupDID, "name": name, "members": group.Members}, nil
}

func (s *Server) joinGroup(params map[string]any) (any, error) {
	group, _ := params["group"].(string)
	if s.groups[group] == nil {
		return nil, fmt.Errorf("group not found")
	}
	return map[string]any{"status": "joined"}, nil
}

func (s *Server) leaveGroup(params map[string]any) (any, error) {
	group, _ := params["group"].(string)
	delete(s.groups, group)
	return map[string]any{"status": "left"}, nil
}

func (s *Server) groupMembers(params map[string]any) (any, error) {
	group, _ := params["group"].(string)
	if s.groups[group] == nil {
		return nil, fmt.Errorf("group not found")
	}
	return map[string]any{"members": []any{}}, nil
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "unknown"
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
