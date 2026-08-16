package identity

import "github.com/datadir-lab/poddle/src/internal/broker"

// FakeProvider is a test Provider. It records whether Authenticate was called
// and returns configurable auth state, credential, and materialization.
type FakeProvider struct {
	ProviderName string
	Authed       bool
	AuthCalled   bool
	Cred         broker.Credential
	Mat          Materialization
}

func (f *FakeProvider) Name() string { return f.ProviderName }

func (f *FakeProvider) Authenticate(id Identity) error {
	f.AuthCalled = true
	f.Authed = true
	return nil
}

func (f *FakeProvider) IsAuthenticated(id Identity) (bool, error) { return f.Authed, nil }

func (f *FakeProvider) Credential(id Identity) (broker.Credential, error) { return f.Cred, nil }

func (f *FakeProvider) Materialize(id Identity) (Materialization, error) { return f.Mat, nil }
