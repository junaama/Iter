package llm

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/iter-dev/iter/pkg/contracts"
)

// StubProvider is a deterministic in-process Provider for tests. Callers
// register canned responses by message-content prefix; the first matching
// prefix wins. If no canned response matches, StubProvider returns
// `Default` (or ErrStubNoMatch if Default is the zero value AND
// ErrOnNoMatch is true).
//
// StubProvider lives in the package because it is part of the test
// scaffolding for the router; routing/breaker tests construct several of
// them to simulate per-provider success/failure patterns. It is NOT
// intended for production wiring — cmd/server does not register it.
type StubProvider struct {
	NameValue    string
	Tiers        []contracts.LLMTier
	Responses    map[string]contracts.LLMCompletionResponse // keyed by message prefix
	Default      contracts.LLMCompletionResponse
	ErrOnNoMatch bool
	FailWith     error // if non-nil, every Complete returns this error

	mu    sync.Mutex
	calls int
}

// ErrStubNoMatch is returned by StubProvider.Complete when no canned
// response matches and ErrOnNoMatch is set.
var ErrStubNoMatch = errors.New("llm/stub: no canned response matches input")

// Name implements Provider.
func (s *StubProvider) Name() string {
	if s.NameValue == "" {
		return "stub"
	}
	return s.NameValue
}

// Supports implements Provider. An empty Tiers slice supports every tier
// (useful for default test setups).
func (s *StubProvider) Supports(tier contracts.LLMTier) bool {
	if len(s.Tiers) == 0 {
		return true
	}
	for _, t := range s.Tiers {
		if t == tier {
			return true
		}
	}
	return false
}

// Calls returns the cumulative number of Complete invocations. Tests use
// this to assert that the router did or did not advance to a particular
// provider in the chain.
func (s *StubProvider) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// Complete implements Provider.
func (s *StubProvider) Complete(_ context.Context, req contracts.LLMCompletionRequest) (contracts.LLMCompletionResponse, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()

	if s.FailWith != nil {
		return contracts.LLMCompletionResponse{}, s.FailWith
	}

	if len(req.Messages) > 0 && s.Responses != nil {
		content := req.Messages[len(req.Messages)-1].Content
		for prefix, resp := range s.Responses {
			if strings.HasPrefix(content, prefix) {
				if resp.Provider == "" {
					resp.Provider = s.Name()
				}
				return resp, nil
			}
		}
	}

	if s.ErrOnNoMatch && s.Default.Text == "" {
		return contracts.LLMCompletionResponse{}, ErrStubNoMatch
	}

	resp := s.Default
	if resp.Provider == "" {
		resp.Provider = s.Name()
	}
	return resp, nil
}
