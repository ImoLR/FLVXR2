package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	// maxFlowUploadBodyBytes bounds one agent flow upload (an item is ~60 bytes).
	maxFlowUploadBodyBytes = 8 << 20
	// flowUploadDedupeTTL is how long an accepted encrypted upload is remembered. Agents
	// resend an unacknowledged upload unchanged for a shorter time than this.
	flowUploadDedupeTTL = 30 * time.Minute
	// flowUploadDedupeMaxEntries caps the remembered uploads (25 nodes x 12/min x 30 min ~ 9k).
	flowUploadDedupeMaxEntries = 200000
)

var errFlowUploadTooLarge = errors.New("flow upload body too large")

func writeFlowUploadText(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(text))
}

func readFlowUploadBody(body io.ReadCloser) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, maxFlowUploadBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxFlowUploadBodyBytes {
		return nil, errFlowUploadTooLarge
	}
	return raw, nil
}

// decodeFlowUploadBody returns the JSON item list of an agent upload. Agents send
// {"encrypted":true,"data":...} (AES-GCM with the node secret) or, without a cipher, the
// plain JSON array. Unlike the lenient config decoder, a body that cannot be decrypted is an
// error, so the agent keeps the bytes instead of the panel dropping them.
//
// dedupeKey identifies an encrypted upload (its ciphertext embeds a random nonce, so equal
// ciphertext means the same upload was sent again); it is empty for plain bodies.
func decodeFlowUploadBody(raw []byte, secret string) (payload []byte, dedupeKey string, err error) {
	text := bytes.TrimSpace(raw)
	if len(text) == 0 {
		return nil, "", nil
	}
	if text[0] != '{' {
		return text, "", nil
	}

	var wrap struct {
		Encrypted bool   `json:"encrypted"`
		Data      string `json:"data"`
	}
	if err := json.Unmarshal(text, &wrap); err != nil {
		return nil, "", fmt.Errorf("parse upload envelope: %w", err)
	}
	if !wrap.Encrypted || wrap.Data == "" {
		return nil, "", errors.New("upload envelope without encrypted data")
	}
	crypto := getOrCreateFlowCrypto(secret)
	if crypto == nil {
		return nil, "", errors.New("flow cipher unavailable for node secret")
	}
	plain, err := crypto.Decrypt(wrap.Data)
	if err != nil {
		return nil, "", fmt.Errorf("decrypt upload: %w", err)
	}
	sum := sha256.Sum256([]byte(wrap.Data))
	return bytes.TrimSpace(plain), hex.EncodeToString(sum[:]), nil
}

func (h *Handler) flowUpload(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repo == nil {
		writeFlowUploadText(w, http.StatusOK, "ok")
		return
	}

	secret := r.URL.Query().Get("secret")
	node, err := h.repo.GetNodeBySecret(secret)
	if err != nil {
		// The node may exist; let the agent keep the bytes and retry.
		logFlowUploadFailure(0, "node lookup", err)
		writeFlowUploadText(w, http.StatusServiceUnavailable, "error")
		return
	}
	if node == nil {
		// Unknown secret (e.g. a deleted node): accept and drop so the agent does not retry forever.
		writeFlowUploadText(w, http.StatusOK, "ok")
		return
	}

	raw, err := readFlowUploadBody(r.Body)
	if err != nil {
		logFlowUploadFailure(node.ID, "read body", err)
		writeFlowUploadText(w, http.StatusBadRequest, "error")
		return
	}
	payload, dedupeKey, err := decodeFlowUploadBody(raw, secret)
	if err != nil {
		logFlowUploadFailure(node.ID, "decode body", err)
		writeFlowUploadText(w, http.StatusBadRequest, "error")
		return
	}
	if len(payload) == 0 {
		writeFlowUploadText(w, http.StatusOK, "ok")
		return
	}
	var items []flowItem
	if err := json.Unmarshal(payload, &items); err != nil {
		logFlowUploadFailure(node.ID, "parse items", err)
		writeFlowUploadText(w, http.StatusBadRequest, "error")
		return
	}

	process := func() error { return h.ingestFlowItems(node.ID, items) }
	if dedupeKey != "" && h.flowUploads != nil {
		err = h.flowUploads.run(strconv.FormatInt(node.ID, 10)+":"+dedupeKey, process)
	} else {
		err = process()
	}
	if err != nil {
		logFlowUploadFailure(node.ID, "ingest", err)
		writeFlowUploadText(w, http.StatusInternalServerError, "error")
		return
	}
	writeFlowUploadText(w, http.StatusOK, "ok")
}

var flowUploadFailureLog = struct {
	sync.Mutex
	last map[string]time.Time
}{last: map[string]time.Time{}}

// logFlowUploadFailure logs at most once per minute per node and stage; a failing agent
// retries every few seconds.
func logFlowUploadFailure(nodeID int64, stage string, err error) {
	key := strconv.FormatInt(nodeID, 10) + ":" + stage
	now := time.Now()
	flowUploadFailureLog.Lock()
	last, seen := flowUploadFailureLog.last[key]
	if seen && now.Sub(last) < time.Minute {
		flowUploadFailureLog.Unlock()
		return
	}
	flowUploadFailureLog.last[key] = now
	flowUploadFailureLog.Unlock()
	log.Printf("flow upload rejected node_id=%d stage=%s err=%v", nodeID, stage, err)
}

// flowUploadDeduper makes the processing of an identical upload idempotent. An agent resends
// an upload when it saw no "ok" (timeout, connection reset, https->http fallback) although the
// panel may already have committed it; counting it again would double the traffic.
type flowUploadDeduper struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	now        func() time.Time
	inflight   map[string]*flowUploadCall
	done       map[string]time.Time
	order      []flowUploadDone
}

type flowUploadCall struct {
	finished chan struct{}
	err      error
}

type flowUploadDone struct {
	key string
	at  time.Time
}

func newFlowUploadDeduper(ttl time.Duration, maxEntries int) *flowUploadDeduper {
	return &flowUploadDeduper{
		ttl:        ttl,
		maxEntries: maxEntries,
		now:        time.Now,
		inflight:   map[string]*flowUploadCall{},
		done:       map[string]time.Time{},
	}
}

// run executes fn at most once per key until the key expires: a key that already succeeded
// is acknowledged without running fn, and a request that arrives while the same key is still
// being processed waits for that result instead of processing it a second time. A failed run
// is forgotten so a resend is processed again.
func (d *flowUploadDeduper) run(key string, fn func() error) (err error) {
	d.mu.Lock()
	d.expireLocked()
	if _, ok := d.done[key]; ok {
		d.mu.Unlock()
		return nil
	}
	if call, ok := d.inflight[key]; ok {
		d.mu.Unlock()
		<-call.finished
		return call.err
	}
	call := &flowUploadCall{finished: make(chan struct{})}
	d.inflight[key] = call
	d.mu.Unlock()

	completed := false
	defer func() {
		if completed {
			return
		}
		rec := recover()
		d.complete(key, call, fmt.Errorf("flow upload processing panicked: %v", rec))
		if rec != nil {
			panic(rec)
		}
	}()
	err = fn()
	completed = true
	d.complete(key, call, err)
	return err
}

func (d *flowUploadDeduper) complete(key string, call *flowUploadCall, err error) {
	d.mu.Lock()
	delete(d.inflight, key)
	if err == nil {
		at := d.now()
		d.done[key] = at
		d.order = append(d.order, flowUploadDone{key: key, at: at})
		d.expireLocked()
	}
	d.mu.Unlock()
	call.err = err
	close(call.finished)
}

func (d *flowUploadDeduper) expireLocked() {
	cutoff := d.now().Add(-d.ttl)
	drop := 0
	for drop < len(d.order) {
		entry := d.order[drop]
		if entry.at.After(cutoff) && len(d.order)-drop <= d.maxEntries {
			break
		}
		if at, ok := d.done[entry.key]; ok && at.Equal(entry.at) {
			delete(d.done, entry.key)
		}
		drop++
	}
	if drop > 0 {
		d.order = append(d.order[:0], d.order[drop:]...)
	}
}
