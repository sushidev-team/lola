package linear

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuth for the Linear AGENT (an app-actor install). Linear's flow is the
// standard authorization-code grant plus `actor=app`, which makes the
// resulting token act as the application's own user — the thing a human can
// assign, delegate to or @mention. The app scopes `app:assignable` and
// `app:mentionable` are what put it in those pickers.
//
// SECRET DISCIPLINE: the client secret, the code and both tokens are request
// BODY fields, never URL parameters, and no error built here ever quotes a
// response body — only the HTTP status and Linear's `error` code, which is a
// fixed vocabulary word (invalid_grant, …) and carries no credential.

const (
	OAuthAuthorizeURL = "https://linear.app/oauth/authorize"
	OAuthTokenURL     = "https://api.linear.app/oauth/token"
	// AgentScopes are the scopes `lola linear-agent login` requests.
	AgentScopes = "read,write,app:assignable,app:mentionable"
)

// OAuthToken is the persisted credential (stored as one JSON keychain item).
type OAuthToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitzero"`
}

// Expired reports whether the access token is past (or within skew of) its
// expiry. A token with no recorded expiry never expires client-side; a 401
// still triggers a refresh.
func (t OAuthToken) Expired(now time.Time, skew time.Duration) bool {
	return !t.ExpiresAt.IsZero() && now.Add(skew).After(t.ExpiresAt)
}

// AgentAuthorizeURL is the browser URL that installs the app as an agent.
// state is the CSRF nonce the callback must echo.
func AgentAuthorizeURL(clientID, redirectURI, state string) string {
	v := url.Values{}
	v.Set("client_id", clientID)
	v.Set("redirect_uri", redirectURI)
	v.Set("response_type", "code")
	v.Set("scope", AgentScopes)
	v.Set("state", state)
	v.Set("actor", "app")
	return OAuthAuthorizeURL + "?" + v.Encode()
}

// OAuthClient exchanges and refreshes tokens. TokenURL and HTTP are seams.
type OAuthClient struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	HTTP         *http.Client
	Now          func() time.Time
}

// Exchange trades an authorization code for tokens.
func (o OAuthClient) Exchange(ctx context.Context, code, redirectURI string) (OAuthToken, error) {
	v := url.Values{}
	v.Set("grant_type", "authorization_code")
	v.Set("code", code)
	v.Set("redirect_uri", redirectURI)
	return o.token(ctx, v)
}

// Refresh trades a refresh token for a fresh access token. Linear rotates the
// refresh token, so the caller must persist the WHOLE result.
func (o OAuthClient) Refresh(ctx context.Context, refreshToken string) (OAuthToken, error) {
	if refreshToken == "" {
		return OAuthToken{}, errors.New("linear oauth: no refresh token stored — run `lola linear-agent login` again")
	}
	v := url.Values{}
	v.Set("grant_type", "refresh_token")
	v.Set("refresh_token", refreshToken)
	t, err := o.token(ctx, v)
	if err == nil && t.RefreshToken == "" {
		t.RefreshToken = refreshToken // a server that does not rotate keeps the old one valid
	}
	return t, err
}

func (o OAuthClient) token(ctx context.Context, v url.Values) (OAuthToken, error) {
	v.Set("client_id", o.ClientID)
	v.Set("client_secret", o.ClientSecret)
	endpoint := o.TokenURL
	if endpoint == "" {
		endpoint = OAuthTokenURL
	}
	hc := o.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(v.Encode()))
	if err != nil {
		return OAuthToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			// *url.Error quotes the URL; ours carries no secret, but keep only the
			// cause so a future endpoint with a query string can never leak one.
			return OAuthToken{}, fmt.Errorf("linear oauth: %w", ue.Err)
		}
		return OAuthToken{}, fmt.Errorf("linear oauth: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return OAuthToken{}, fmt.Errorf("linear oauth: read response: %w", err)
	}
	var r struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
	}
	_ = json.Unmarshal(body, &r)
	if resp.StatusCode != http.StatusOK || r.AccessToken == "" {
		code := oauthErrorCode(r.Error)
		if code == "" {
			code = "no access token in response"
		}
		return OAuthToken{}, fmt.Errorf("linear oauth: http %d: %s", resp.StatusCode, code)
	}
	t := OAuthToken{AccessToken: r.AccessToken, RefreshToken: r.RefreshToken}
	if r.ExpiresIn > 0 {
		t.ExpiresAt = now().Add(time.Duration(r.ExpiresIn) * time.Second)
	}
	return t, nil
}

// oauthErrorCode keeps Linear's `error` field only when it is a plain OAuth
// error word, so a server that echoed something unexpected into it can never
// put that into a log line.
func oauthErrorCode(s string) string {
	if len(s) == 0 || len(s) > 40 {
		return ""
	}
	for _, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z') {
			return ""
		}
	}
	return s
}
