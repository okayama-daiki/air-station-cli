package airstation

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const sessionTTL = 5 * time.Minute
const macRegPageTTL = 30 * time.Second

type savedSession struct {
	SavedAt time.Time     `json:"saved_at"`
	URL     string        `json:"url"`
	Cookies []savedCookie `json:"cookies"`
}

type savedCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func sessionFilePath(baseURL *url.URL) (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	safe := strings.NewReplacer(":", "_", ".", "_").Replace(baseURL.Host)
	return filepath.Join(cacheDir, "air-station", "session-"+safe+".json"), nil
}

func macRegPageFilePath(baseURL *url.URL) (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	safe := strings.NewReplacer(":", "_", ".", "_").Replace(baseURL.Host)
	return filepath.Join(cacheDir, "air-station", "mac-reg-page-"+safe+".json"), nil
}

type savedMacRegPage struct {
	SavedAt time.Time `json:"saved_at"`
	URL     string    `json:"url"`
	HTML    string    `json:"html"`
}

func (c *Client) saveMacRegPage(page *Page) {
	if c.macRegPageFile == "" {
		return
	}
	html, err := page.Doc.Html()
	if err != nil {
		return
	}
	saved := savedMacRegPage{
		SavedAt: time.Now(),
		URL:     page.URL.String(),
		HTML:    html,
	}
	data, err := json.Marshal(saved)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.macRegPageFile), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(c.macRegPageFile, data, 0o600)
	c.logDebug("mac-reg-page: saved to %s", c.macRegPageFile)
}

func (c *Client) loadMacRegPage() {
	if c.macRegPageFile == "" {
		return
	}
	data, err := os.ReadFile(c.macRegPageFile)
	if err != nil {
		return
	}
	var saved savedMacRegPage
	if err := json.Unmarshal(data, &saved); err != nil {
		return
	}
	age := time.Since(saved.SavedAt)
	if age > macRegPageTTL {
		c.logDebug("mac-reg-page: expired (age=%s)", age.Round(time.Second))
		_ = os.Remove(c.macRegPageFile)
		return
	}
	pageURL, err := url.Parse(saved.URL)
	if err != nil {
		return
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(saved.HTML))
	if err != nil {
		return
	}
	c.macRegPage = &Page{URL: pageURL, Doc: doc}
	c.logDebug("mac-reg-page: reusing cached page (age=%s)", age.Round(time.Second))
}

func (c *Client) deleteMacRegPage() {
	if c.macRegPageFile == "" {
		return
	}
	_ = os.Remove(c.macRegPageFile)
}

func (c *Client) loadSession() {
	if c.sessionFile == "" {
		return
	}
	data, err := os.ReadFile(c.sessionFile)
	if err != nil {
		return
	}
	var session savedSession
	if err := json.Unmarshal(data, &session); err != nil {
		return
	}
	age := time.Since(session.SavedAt)
	if age > sessionTTL {
		c.logDebug("session: expired (age=%s)", age.Round(time.Second))
		return
	}
	target, err := url.Parse(session.URL)
	if err != nil {
		return
	}
	cookies := make([]*http.Cookie, 0, len(session.Cookies))
	for _, sc := range session.Cookies {
		cookies = append(cookies, &http.Cookie{Name: sc.Name, Value: sc.Value})
	}
	c.jar.SetCookies(target, cookies)
	c.loggedIn = true
	c.logDebug("session: reusing cached session (age=%s)", age.Round(time.Second))
}

func (c *Client) saveSession() {
	if c.sessionFile == "" {
		return
	}
	cookies := c.jar.Cookies(c.baseURL)
	if len(cookies) == 0 {
		return
	}
	saved := make([]savedCookie, 0, len(cookies))
	for _, cookie := range cookies {
		saved = append(saved, savedCookie{Name: cookie.Name, Value: cookie.Value})
	}
	session := savedSession{
		SavedAt: time.Now(),
		URL:     c.baseURL.String(),
		Cookies: saved,
	}
	data, err := json.Marshal(session)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.sessionFile), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(c.sessionFile, data, 0o600)
	c.logDebug("session: saved to %s", c.sessionFile)
}

func (c *Client) deleteSession() {
	if c.sessionFile == "" {
		return
	}
	_ = os.Remove(c.sessionFile)
	c.logDebug("session: deleted %s", c.sessionFile)
}
