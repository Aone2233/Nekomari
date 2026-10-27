package api

import (
	"errors"
	"testing"

	"github.com/Aone2233/nekomari/pkg/rpc"
)

func TestClientOperationFailsClosedWithoutMatchingIdentity(t *testing.T) {
	for name, principal := range map[string]*rpc.Principal{
		"missing": nil, "anonymous": rpc.NewAnonymousPrincipal(),
		"api-key":    rpc.NewScopedAPIKeyPrincipal("fixture-key", rpc.APIKeyScopes()),
		"other-node": rpc.NewAgentPrincipal("other-node"), "empty-user": rpc.NewUserPrincipal(""),
	} {
		t.Run(name, func(t *testing.T) {
			c := newAuthTestContext("POST", "/api/clients/v2/rpc", nil)
			if principal != nil {
				SetPrincipal(c, principal)
			}
			if err := ClientOperation(c, "node")(t.Context(), func() error {
				t.Error("unmatched identity reached side effects")
				return nil
			}); !errors.Is(err, ErrClientCredentialRetired) {
				t.Fatalf("admission error = %v", err)
			}
		})
	}
}
