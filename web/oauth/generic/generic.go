package generic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Aone2233/nekomari/utils"
	"github.com/Aone2233/nekomari/web/oauth/factory"
	"github.com/Aone2233/nekomari/web/oauth/internal/oauthutil"
	"github.com/gin-gonic/gin"
)

func (g *Generic) GetName() string {
	return "generic"
}
func (g *Generic) GetConfiguration() factory.Configuration {
	return &g.Addition
}

func (g *Generic) GetAuthorizationURL(redirectURI string) (string, string) {
	state := utils.GenerateRandomString(16)

	// 构建GitHub OAuth授权URL
	authURL := fmt.Sprintf(
		"%s?client_id=%s&state=%s&scope=%s&redirect_uri=%s&response_type=code",
		g.Addition.AuthURL,
		url.QueryEscape(g.Addition.ClientId),
		url.QueryEscape(state),
		url.QueryEscape(g.Addition.Scope),
		url.QueryEscape(redirectURI),
	)
	if !g.stateCache.Add(state) {
		return "", ""
	}
	return authURL, state
}
func (g *Generic) OnCallback(ctx *gin.Context, state string, query map[string]string, callbackURI string) (factory.OidcCallback, error) {
	code := query["code"]

	// 验证state防止CSRF攻击
	if !g.stateCache.Consume(state) {
		return factory.OidcCallback{}, fmt.Errorf("invalid state")
	}

	// 获取code
	if code == "" {
		return factory.OidcCallback{}, fmt.Errorf("no code provided")
	}

	// 获取访问令牌
	data := url.Values{
		"client_id":     {g.Addition.ClientId},
		"client_secret": {g.Addition.ClientSecret},
		"code":          {code},
		"redirect_uri":  {callbackURI},
		"grant_type":    {"authorization_code"},
	}

	req, err := http.NewRequestWithContext(ctx.Request.Context(), "POST", g.Addition.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return factory.OidcCallback{}, fmt.Errorf("invalid token URL")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}

	if err := oauthutil.JSON(req, &tokenResp); err != nil {
		return factory.OidcCallback{}, err
	}
	if tokenResp.AccessToken == "" {
		return factory.OidcCallback{}, fmt.Errorf("empty access token")
	}

	// 获取用户信息
	userReq, err := http.NewRequestWithContext(ctx.Request.Context(), "GET", g.Addition.UserInfoURL, nil)
	if err != nil {
		return factory.OidcCallback{}, fmt.Errorf("invalid user info URL")
	}
	userReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	userReq.Header.Set("Accept", "application/json")

	var user map[string]json.RawMessage
	if err := oauthutil.JSON(userReq, &user); err != nil {
		return factory.OidcCallback{}, err
	}

	userId, ok := user[g.Addition.UserIDField]
	if !ok {
		return factory.OidcCallback{}, fmt.Errorf("user id field '%s' not found in user info response", g.Addition.UserIDField)
	}

	var id string
	if err := json.Unmarshal(userId, &id); err != nil {
		var number float64
		if err := json.Unmarshal(userId, &number); err != nil || string(userId) == "null" {
			return factory.OidcCallback{}, fmt.Errorf("invalid user id")
		}
		// Existing account bindings store this representation. Changing numeric
		// formatting requires a migration; providers should prefer string IDs.
		id = fmt.Sprint(number)
	}
	if strings.TrimSpace(id) == "" {
		return factory.OidcCallback{}, fmt.Errorf("empty user id")
	}
	return factory.OidcCallback{UserId: id}, nil
}
func (g *Generic) Init() error {
	g.stateCache.Clear()
	return nil
}
func (g *Generic) Destroy() error {
	g.stateCache.Clear()
	return nil
}

var _ factory.IOidcProvider = (*Generic)(nil)
