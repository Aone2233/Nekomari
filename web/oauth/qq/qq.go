package qq

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/Aone2233/nekomari/utils"
	"github.com/Aone2233/nekomari/web/oauth/factory"
	"github.com/Aone2233/nekomari/web/oauth/internal/oauthutil"
	"github.com/gin-gonic/gin"
)

func (q *QQ) GetName() string {
	return "qq"
}

func (q *QQ) GetConfiguration() factory.Configuration {
	return &q.Addition
}

func (q *QQ) GetAuthorizationURL(redirectURI string) (string, string) {
	state := utils.GenerateRandomString(16)
	if !q.stateCache.Add(state) {
		return "", ""
	}
	// Aggregators that do not echo state still preserve the callback query.
	callback, err := url.Parse(redirectURI)
	if err != nil {
		q.stateCache.Consume(state)
		return "", ""
	}
	values := callback.Query()
	values.Set("state", state)
	callback.RawQuery = values.Encode()

	// 构建请求QQ聚合登录平台的URL
	requestURL := fmt.Sprintf(
		"%s/connect.php?act=login&appid=%s&appkey=%s&type=%s&redirect_uri=%s",
		q.Addition.AggregationURL,
		url.QueryEscape(q.Addition.AppId),
		url.QueryEscape(q.Addition.AppKey),
		url.QueryEscape(q.Addition.LoginType),
		url.QueryEscape(callback.String()),
	)

	// 向聚合登录平台发送请求
	// 解析响应JSON
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		URL  string `json:"url"`
	}

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		q.stateCache.Consume(state)
		return "", ""
	}
	if err := oauthutil.JSON(req, &result); err != nil || result.Code != 0 || result.URL == "" {
		q.stateCache.Consume(state)
		return "", ""
	}
	return result.URL, state
}

// OnCallback 处理QQ OAuth回调
// 例如：http://localhost:25774/api/oauth_callback?type=qq&code=XXXXXXXXXXXXXXXX
// 然后我们使用code参数向聚合登录平台请求用户信息
func (q *QQ) OnCallback(ctx *gin.Context, state string, query map[string]string, callbackURI string) (factory.OidcCallback, error) {
	// 根据文档，回调地址会附带type和code参数
	code := query["code"]
	loginType := query["type"]

	// 如果回调中没有type参数，则使用配置中的LoginType
	if loginType == "" {
		loginType = q.Addition.LoginType
	}

	// 验证state防止CSRF攻击
	if !q.stateCache.Consume(state) {
		return factory.OidcCallback{}, fmt.Errorf("invalid state")
	}

	// 检查是否提供了Authorization Code
	if code == "" {
		return factory.OidcCallback{}, fmt.Errorf("no authorization code provided")
	}

	// 通过Authorization Code获取用户信息
	// 根据文档，请求URL应为: {AggregationURL}/connect.php?act=callback&appid={appid}&appkey={appkey}&type={登录方式}&code={code}
	callbackURL := fmt.Sprintf(
		"%s/connect.php?act=callback&appid=%s&appkey=%s&type=%s&code=%s",
		q.Addition.AggregationURL,
		url.QueryEscape(q.Addition.AppId),
		url.QueryEscape(q.Addition.AppKey),
		url.QueryEscape(loginType),
		url.QueryEscape(code),
	)

	// 解析响应
	var result struct {
		Code        int    `json:"code"`
		Msg         string `json:"msg"`
		Type        string `json:"type"`
		SocialUid   string `json:"social_uid"`
		AccessToken string `json:"access_token"`
		FaceImg     string `json:"faceimg"`
		Nickname    string `json:"nickname"`
		Gender      string `json:"gender"`
		Location    string `json:"location"`
		IP          string `json:"ip"`
	}

	req, err := http.NewRequestWithContext(ctx.Request.Context(), "GET", callbackURL, nil)
	if err != nil {
		return factory.OidcCallback{}, fmt.Errorf("invalid callback URL")
	}
	if err := oauthutil.JSON(req, &result); err != nil {
		return factory.OidcCallback{}, err
	}

	// 检查返回状态码
	if result.Code != 0 {
		return factory.OidcCallback{}, fmt.Errorf("QQ login callback failed with code %d", result.Code)
	}

	// 检查是否返回了用户唯一标识
	if result.SocialUid == "" {
		return factory.OidcCallback{}, fmt.Errorf("empty social_uid returned")
	}

	// 返回用户唯一标识
	return factory.OidcCallback{UserId: result.SocialUid}, nil
}

func (q *QQ) Init() error {
	q.stateCache.Clear()
	return nil
}

func (q *QQ) Destroy() error {
	q.stateCache.Clear()
	return nil
}

var _ factory.IOidcProvider = (*QQ)(nil)
