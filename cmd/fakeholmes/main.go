// Command fakeholmes is a development stand-in for HolmesGPT's POST /api/chat (streaming).
// It answers investigations with a canned analysis and proposes safe actions, and replies to chat.
//
//	go run ./cmd/fakeholmes -listen :9099
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"
)

type chatReq struct {
	Ask                 string          `json:"ask"`
	Model               string          `json:"model"`
	ConversationHistory json.RawMessage `json:"conversation_history"`
	Stream              bool            `json:"stream"`
	ResponseFormat      json.RawMessage `json:"response_format"`
}

func main() {
	listen := flag.String("listen", ":9099", "listen address")
	delay := flag.Duration("delay", 2*time.Second, "simulated thinking time")
	flag.Parse()
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	http.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		var req chatReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"detail":"bad json"}`, http.StatusBadRequest)
			return
		}
		investigation := len(req.ConversationHistory) == 0 || string(req.ConversationHistory) == "null"
		var answer any
		if investigation {
			answer = map[string]any{
				"verdict": "actionable", "confidence": 0.82,
				"summary":    "The pod is crash looping after an OOM kill; memory usage grew after the last deploy.",
				"root_cause": "Memory limit (256Mi) too low for the new release.",
				"analysis":   "## Findings\n- `kubectl describe pod` shows **OOMKilled** (exit 137)\n- Restarts started 10 minutes after the rollout\n\n## Suggestion\nRestart now to recover, then raise the memory limit to 512Mi.",
				"proposed_actions": []map[string]any{
					{"type": "agent.reinvestigate", "params_json": `{"reason":"check after remediation"}`, "rationale": "Re-check once the pod is stable", "risk": "low"},
				},
			}
		} else {
			answer = map[string]any{"reply": "Based on the logs, the container exceeds its 256Mi limit about 2 minutes after start. Raising the limit should fix it.", "proposed_actions": []any{}}
		}
		analysis, _ := json.Marshal(answer)
		history := []map[string]string{{"role": "system", "content": "fake holmes"}, {"role": "user", "content": req.Ask}, {"role": "assistant", "content": string(analysis)}}
		if !req.Stream {
			_ = json.NewEncoder(w).Encode(map[string]any{"analysis": string(analysis), "conversation_history": history, "tool_calls": []any{}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		send := func(ev string, v any) {
			b, _ := json.Marshal(v)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev, b)
			if fl != nil {
				fl.Flush()
			}
		}
		send("start_tool_calling", map[string]any{"tool_name": "kubectl_describe", "id": "t1"})
		time.Sleep(*delay / 2)
		send("tool_calling_result", map[string]any{"tool_call_id": "t1", "name": "kubectl_describe", "description": "kubectl describe pod",
			"result": map[string]any{"status": "success", "data": "Last State: Terminated, Reason: OOMKilled, Exit Code: 137"}})
		send("ai_message", map[string]any{"content": "The container was OOM killed; checking memory usage.", "reasoning": nil})
		time.Sleep(*delay / 2)
		send("ai_answer_end", map[string]any{"analysis": string(analysis), "conversation_history": history,
			"metadata": map[string]any{"usage": map[string]int{"prompt_tokens": 1200, "completion_tokens": 300, "total_tokens": 1500}}})
	})
	log.Printf("fake holmes listening on %s", *listen)
	srv := &http.Server{Addr: *listen, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
