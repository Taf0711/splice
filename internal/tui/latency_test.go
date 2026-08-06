package tui

import (
	"context"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/sessions"
)

func TestAvgTurnLatencyText(t *testing.T) {
	m := newModel(context.Background(), Options{})
	if got := m.avgTurnLatencyText(); got != "n/a" {
		t.Fatalf("empty latency = %q, want n/a", got)
	}
	m.turnLatencySum = 9 * time.Second
	m.turnLatencyCount = 2
	if got := m.avgTurnLatencyText(); got != "4.5s avg (2 turns)" {
		t.Fatalf("avgTurnLatencyText = %q, want \"4.5s avg (2 turns)\"", got)
	}
	// With TTFT recorded, the line also reports time-to-first-token.
	m.turnTTFTSum = 3 * time.Second
	m.turnTTFTCount = 2
	if got := m.avgTurnLatencyText(); got != "4.5s avg (1.5s to first token, 2 turns)" {
		t.Fatalf("avgTurnLatencyText with ttft = %q, want \"4.5s avg (1.5s to first token, 2 turns)\"", got)
	}
	m.turnVisibleOutputTokens = 100
	if got := m.avgTurnLatencyText(); got != "4.5s avg (1.5s to first token, 16.7 TPS avg across turns, 2 turns)" {
		t.Fatalf("avgTurnLatencyText with throughput = %q, want generation-time TPS clause", got)
	}

	for _, tc := range []struct {
		name string
		m    model
		want string
	}{
		{name: "elapsed equals ttft", m: model{turnLatencySum: time.Second, turnLatencyCount: 1, turnTTFTSum: time.Second, turnTTFTCount: 1, turnVisibleOutputTokens: 10}, want: "1.0s avg (1.0s to first token, 1 turns)"},
		{name: "zero tokens", m: model{turnLatencySum: 2 * time.Second, turnLatencyCount: 1, turnTTFTSum: time.Second, turnTTFTCount: 1}, want: "2.0s avg (1.0s to first token, 1 turns)"},
		{name: "zero turns", m: model{turnVisibleOutputTokens: 10}, want: "n/a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.m.avgTurnLatencyText(); got != tc.want {
				t.Fatalf("avgTurnLatencyText = %q, want %q", got, tc.want)
			}
		})
	}
	// /new must reset the rolling latency + ttft so a fresh session starts from splice.
	m.activeSession = sessions.Metadata{SessionID: "x"}
	next := m.startNewSession()
	if next.turnLatencyCount != 0 || next.turnLatencySum != 0 || next.turnTTFTCount != 0 || next.turnTTFTSum != 0 || next.turnVisibleOutputTokens != 0 {
		t.Fatalf("startNewSession must reset latency+ttft+throughput, got latency=%d/%v ttft=%d/%v tokens=%d", next.turnLatencyCount, next.turnLatencySum, next.turnTTFTCount, next.turnTTFTSum, next.turnVisibleOutputTokens)
	}
}
