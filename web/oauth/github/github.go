package github

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Aone2233/nekomari/utils"
	"github.com/Aone2233/nekomari/web/oauth/factory"
	"github.com/Aone2233/nekomari/web/oauth/internal/oauthutil"
	"github.com/gin-gonic/gin"
)

func init() {

}

func (g *Github) GetName() string {
	return "github"
}
func (g *Github) GetConfiguration() factory.Configuration {
	return &g.Addition
}

func (g *Github) GetAuthorizationURL(_ string) (string, string) {
	state := utils.GenerateRandomString(16)

	// 构建GitHub OAuth授权URL
	authURL := fmt.Sprintf(
		"https://github.com/login/oauth/authorize?client_id=%s&state=%s&scope=user:email",
		url.QueryEscape(g.Addition.ClientId),
		url.QueryEscape(state),
	)
	if !g.stateCache.Add(state) {
		return "", ""
	}
	return authURL, state
}
func (g *Github) OnCallback(ctx *gin.Context, state string, query map[string]string, _ string) (factory.OidcCallback, error) {
	code := query["code"]

	// 验证state防止CSRF攻击
	// state, _ := c.Cookie("oauth_state")
	if !g.stateCache.Consume(state) {
		return factory.OidcCallback{}, fmt.Errorf("invalid state")
	}

	// 获取code
	//code := c.Query("code")
	if code == "" {
		return factory.OidcCallback{}, fmt.Errorf("no code provided")
	}

	// 获取访问令牌
	tokenURL := "https://github.com/login/oauth/access_token"
	data := url.Values{
		"client_id":     {g.Addition.ClientId},
		"client_secret": {g.Addition.ClientSecret},
		"code":          {code},
	}

	req, _ := http.NewRequestWithContext(ctx.Request.Context(), "POST", tokenURL, strings.NewReader(data.Encode()))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Scope       string `json:"scope"`
	}

	if err := oauthutil.JSON(req, &tokenResp); err != nil {
		return factory.OidcCallback{}, err
	}
	if tokenResp.AccessToken == "" {
		return factory.OidcCallback{}, fmt.Errorf("empty access token")
	}

	// 获取用户信息
	userReq, _ := http.NewRequestWithContext(ctx.Request.Context(), "GET", "https://api.github.com/user", nil)
	userReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	userReq.Header.Set("Accept", "application/json")

	var githubUser GitHubUser
	if err := oauthutil.JSON(userReq, &githubUser); err != nil {
		return factory.OidcCallback{}, err
	}
	if githubUser.ID <= 0 {
		return factory.OidcCallback{}, fmt.Errorf("invalid user id")
	}

	return factory.OidcCallback{UserId: fmt.Sprintf("%d", githubUser.ID)}, nil
}
func (g *Github) Init() error {
	g.stateCache.Clear()
	return nil
}
func (g *Github) Destroy() error {
	g.stateCache.Clear()
	return nil
}

var _ factory.IOidcProvider = (*Github)(nil)
