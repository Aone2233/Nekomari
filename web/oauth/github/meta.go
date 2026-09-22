package github

import (
	"github.com/Aone2233/nekomari/web/oauth/factory"
	"github.com/Aone2233/nekomari/web/oauth/internal/oauthutil"
)

func init() {
	factory.RegisterOidcProvider(func() factory.IOidcProvider {
		return &Github{}
	})
}

type Github struct {
	Addition
	stateCache oauthutil.States
}

type Addition struct {
	ClientId     string `json:"client_id" required:"true"`
	ClientSecret string `json:"client_secret" required:"true"`
}

type GitHubUser struct {
	ID    int    `json:"id"`
	Login string `json:"login"`
	Email string `json:"email"`
}
