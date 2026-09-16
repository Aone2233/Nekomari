package oauth

import (
	_ "github.com/Aone2233/nekomari/web/oauth/factory"
	_ "github.com/Aone2233/nekomari/web/oauth/generic"
	_ "github.com/Aone2233/nekomari/web/oauth/github"
	_ "github.com/Aone2233/nekomari/web/oauth/qq"
)

func All() {
	//empty function to ensure all OIDC providers are registered
}
