package main

import (
	"strings"
	"testing"
)

func TestUpsertRequestValidate(t *testing.T) {
	tests := []struct {
		name    string
		req     upsertRequest
		wantErr string
	}{
		{
			name: "valid minimal",
			req: upsertRequest{
				OwnerAgent: "agent-1",
				Visibility: "private",
				MemoryType: "decision",
				Title:      "title",
				Content:    "content",
			},
			wantErr: "",
		},
		{
			name: "valid with optional fields",
			req: upsertRequest{
				OwnerAgent: "agent-1",
				Visibility: "shareable",
				MemoryType: "discovery",
				Title:      "title",
				Content:    "content",
				Scope:      "global",
				Confidence: ptr(0.5),
			},
			wantErr: "",
		},
		{
			name: "empty owner_agent",
			req: upsertRequest{
				OwnerAgent: "",
				Visibility: "private",
				MemoryType: "decision",
				Title:      "title",
				Content:    "content",
			},
			wantErr: "owner_agent is required",
		},
		{
			name: "bad visibility",
			req: upsertRequest{
				OwnerAgent: "agent-1",
				Visibility: "public",
				MemoryType: "decision",
				Title:      "title",
				Content:    "content",
			},
			wantErr: `visibility must be 'private' or 'shareable'`,
		},
		{
			name: "bad scope",
			req: upsertRequest{
				OwnerAgent: "agent-1",
				Visibility: "private",
				MemoryType: "decision",
				Title:      "title",
				Content:    "content",
				Scope:      "team",
			},
			wantErr: `scope must be 'project' or 'global'`,
		},
		{
			name: "empty content",
			req: upsertRequest{
				OwnerAgent: "agent-1",
				Visibility: "private",
				MemoryType: "decision",
				Title:      "title",
				Content:    "",
			},
			wantErr: "content is required",
		},
		{
			name: "empty memory_type",
			req: upsertRequest{
				OwnerAgent: "agent-1",
				Visibility: "private",
				MemoryType: "",
				Title:      "title",
				Content:    "content",
			},
			wantErr: "memory_type is required",
		},
		{
			name: "empty title",
			req: upsertRequest{
				OwnerAgent: "agent-1",
				Visibility: "private",
				MemoryType: "decision",
				Title:      "",
				Content:    "content",
			},
			wantErr: "title is required",
		},
		{
			name: "confidence too low",
			req: upsertRequest{
				OwnerAgent: "agent-1",
				Visibility: "private",
				MemoryType: "decision",
				Title:      "title",
				Content:    "content",
				Confidence: ptr(-0.1),
			},
			wantErr: "confidence must be in [0, 1]",
		},
		{
			name: "confidence too high",
			req: upsertRequest{
				OwnerAgent: "agent-1",
				Visibility: "private",
				MemoryType: "decision",
				Title:      "title",
				Content:    "content",
				Confidence: ptr(1.5),
			},
			wantErr: "confidence must be in [0, 1]",
		},
		{
			name: "empty scope defaults to project",
			req: upsertRequest{
				OwnerAgent: "agent-1",
				Visibility: "private",
				MemoryType: "decision",
				Title:      "title",
				Content:    "content",
				Scope:      "",
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want to contain %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestSearchRequestValidate(t *testing.T) {
	tests := []struct {
		name     string
		req      searchRequest
		wantErr  string
		wantDiff int // expected Limit after clamping
	}{
		{
			name: "valid minimal",
			req: searchRequest{
				Query:           "test",
				RequestingAgent: "agent-1",
			},
			wantErr:  "",
			wantDiff: 0, // handler will default to 8
		},
		{
			name: "empty query",
			req: searchRequest{
				Query:           "",
				RequestingAgent: "agent-1",
			},
			wantErr: "query is required",
		},
		{
			name: "empty requesting_agent",
			req: searchRequest{
				Query:           "test",
				RequestingAgent: "",
			},
			wantErr: "requesting_agent is required",
		},
		{
			name: "limit clamped to 100",
			req: searchRequest{
				Query:           "test",
				RequestingAgent: "agent-1",
				Limit:           500,
			},
			wantErr:  "",
			wantDiff: 100,
		},
		{
			name: "limit 50 is unchanged",
			req: searchRequest{
				Query:           "test",
				RequestingAgent: "agent-1",
				Limit:           50,
			},
			wantErr:  "",
			wantDiff: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				if tt.wantDiff != 0 && tt.req.Limit != tt.wantDiff {
					t.Fatalf("after clamp: Limit = %d, want %d", tt.req.Limit, tt.wantDiff)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want to contain %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestMarkReviewedRequestValidate(t *testing.T) {
	tests := []struct {
		name    string
		req     markReviewedRequest
		wantErr string
	}{
		{
			name:    "valid id",
			req:     markReviewedRequest{ID: 1},
			wantErr: "",
		},
		{
			name:    "id zero",
			req:     markReviewedRequest{ID: 0},
			wantErr: "id must be >= 1",
		},
		{
			name:    "negative id",
			req:     markReviewedRequest{ID: -1},
			wantErr: "id must be >= 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want to contain %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
