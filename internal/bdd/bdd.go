// Package bdd provides a zero-dependency Go BDD test harness and pt-BR Gherkin
// specification parser and contract validator.
package bdd

import (
	"fmt"
	"testing"
)

// StepContext carries the active testing handle across sequential BDD steps.
type StepContext struct {
	T *testing.T
}

// Feature runs fn as a named subtest representing a BDD feature.
func Feature(t *testing.T, id, title string, fn func(t *testing.T)) {
	t.Helper()
	t.Run(fmt.Sprintf("%s: %s", id, title), fn)
}

// Scenario runs fn as a named subtest with a fresh StepContext.
func Scenario(t *testing.T, id, title string, fn func(s *StepContext)) {
	t.Helper()
	t.Run(fmt.Sprintf("%s: %s", id, title), func(t *testing.T) {
		t.Helper()
		fn(&StepContext{T: t})
	})
}

// Given executes a precondition step.
func (s *StepContext) Given(desc string, fn func()) {
	s.T.Helper()
	s.runStep("Dado", desc, fn)
}

// When executes an action step.
func (s *StepContext) When(desc string, fn func()) {
	s.T.Helper()
	s.runStep("Quando", desc, fn)
}

// Then executes an assertion step.
func (s *StepContext) Then(desc string, fn func()) {
	s.T.Helper()
	s.runStep("Então", desc, fn)
}

// And executes a continuation step.
func (s *StepContext) And(desc string, fn func()) {
	s.T.Helper()
	s.runStep("E", desc, fn)
}

func (s *StepContext) runStep(keyword, desc string, fn func()) {
	s.T.Helper()
	s.T.Logf("[%s] %s", keyword, desc)
	fn()
}
