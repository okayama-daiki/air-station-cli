package airstation

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"os"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	defaultBaseURL  = "http://192.168.11.1"
	defaultUsername = "admin"

	loginPath    = "/cgi-bin/cgi?req=twz"
	advancedPath = "/cgi-bin/cgi?req=frm&frm=advanced.html&CAT=DIAG&ITEM=SYSTEM"

	desktopUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
)

var (
	modulusPattern  = regexp.MustCompile(`var modulus = "([0-9A-F]+)"`)
	exponentPattern = regexp.MustCompile(`var exponent = "([0-9]+)"`)
	macPattern      = regexp.MustCompile(`^[0-9A-F]{2}(?::[0-9A-F]{2}){5}$`)
	deletePattern   = regexp.MustCompile(`^DELETE(\d+)$`)
	delPattern      = regexp.MustCompile(`^DEL(-?\d+)$`)
	editTagPattern  = regexp.MustCompile(`edittag=(-?\d+)`)
)

type Config struct {
	BaseURL   string
	Username  string
	Password  string
	Timeout   time.Duration
	UserAgent string
}

func DefaultConfig() Config {
	return Config{
		BaseURL:   defaultBaseURL,
		Username:  defaultUsername,
		Timeout:   15 * time.Second,
		UserAgent: desktopUserAgent,
	}
}

type Client struct {
	baseURL    *url.URL
	username   string
	password   string
	userAgent  string
	jar        http.CookieJar
	timeout    time.Duration
	loggedIn   bool
	macRegPage *Page // cached POST response for chaining mac_reg.html operations
}

type MacFilterState struct {
	Enabled24 bool       `json:"enabled24"`
	Enabled5  bool       `json:"enabled5"`
	Entries   []MacEntry `json:"entries"`
}

type MacEntry struct {
	MAC       string `json:"mac"`
	Connected bool   `json:"connected"`
}

type DHCPAssignment struct {
	IP            string `json:"ip"`
	MAC           string `json:"mac"`
	Lease         string `json:"lease"`
	Status        string `json:"status"`
	DeleteIndex   *int   `json:"deleteIndex"`
	EditTag       *int   `json:"editTag"`
	CurrentClient bool   `json:"currentClient"`
}

type Page struct {
	URL *url.URL
	Doc *goquery.Document
}

type MacRegistryEntry struct {
	MAC   string
	Index int
}

func NewClient(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Username == "" {
		cfg.Username = defaultUsername
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = desktopUserAgent
	}

	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}

	return &Client{
		baseURL:   baseURL,
		username:  cfg.Username,
		password:  cfg.Password,
		userAgent: cfg.UserAgent,
		jar:       jar,
		timeout:   cfg.Timeout,
	}, nil
}

func (c *Client) Login(ctx context.Context) error {
	if c.loggedIn {
		return nil
	}
	if c.password == "" {
		return errors.New("password is required: set --password or AIR_STATION_PASSWORD")
	}

	loginPage, html, err := c.fetchPage(ctx, loginPath, nil)
	if err != nil {
		return err
	}
	form, err := findForm(loginPage.Doc, func(form *Form) bool {
		return form.HasControl("airstation_uname") && form.HasControl("airstation_pass") && form.HasControl("encrypted")
	})
	if err != nil {
		return fmt.Errorf("login form not found: %w", err)
	}

	modulus, exponent, err := extractLoginKey(html)
	if err != nil {
		return err
	}
	encrypted, err := encryptPassword(modulus, exponent, buildEncryptedPayload("airstation_pass", c.password))
	if err != nil {
		return err
	}

	values := form.Values("")
	values.Set("airstation_uname", c.username)
	values.Del("airstation_pass")
	values.Set("encrypted", encrypted)

	if err := c.submitForm(ctx, loginPage, form, values, ""); err != nil {
		return err
	}

	verifyPage, _, err := c.fetchPage(ctx, advancedPath, nil)
	if err != nil {
		return err
	}
	if isLoginPage(verifyPage.Doc) {
		// Re-fetch the login page to check if another user is already logged in
		loginCheck, _, err := c.fetchPage(ctx, loginPath, nil)
		if err == nil && isAnotherUserLoggedIn(loginCheck.Doc) {
			return errors.New("login failed: another user is already logged in")
		}
		return fmt.Errorf("login failed: invalid username or password (username: %s)", c.username)
	}
	c.loggedIn = true
	return nil
}

func (c *Client) ReadMacFiltering(ctx context.Context) (*MacFilterState, error) {
	page, err := c.getAuthenticatedPage(ctx, "/cgi-bin/cgi?req=frm&frm=mac.html")
	if err != nil {
		return nil, err
	}

	state := &MacFilterState{
		Enabled24: page.Doc.Find(`input[name="macmode_11bg"][checked]`).Length() > 0,
		Enabled5:  page.Doc.Find(`input[name="macmode_11a"][checked]`).Length() > 0,
	}

	page.Doc.Find("table.AD_LIST tr").Each(func(index int, row *goquery.Selection) {
		if index == 0 {
			return
		}
		cells := row.Find("td")
		if cells.Length() < 2 {
			return
		}
		mac := NormalizeMAC(cells.Eq(0).Text())
		if !IsMACAddress(mac) {
			return
		}
		status := strings.TrimSpace(cells.Eq(1).Text())
		state.Entries = append(state.Entries, MacEntry{
			MAC:       mac,
			Connected: strings.Contains(status, "◯") || strings.Contains(status, "○"),
		})
	})

	return state, nil
}

func (c *Client) SetMacFiltering(ctx context.Context, enabled24, enabled5 *bool) (*MacFilterState, error) {
	page, err := c.getAuthenticatedPage(ctx, "/cgi-bin/cgi?req=frm&frm=mac.html")
	if err != nil {
		return nil, err
	}
	form, err := findForm(page.Doc, func(form *Form) bool {
		return form.HasControl("macmode_11bg") && form.HasControl("macmode_11a")
	})
	if err != nil {
		return nil, fmt.Errorf("MAC settings form not found: %w", err)
	}

	values := form.Values("")
	if enabled24 != nil {
		setCheckbox(values, form, "macmode_11bg", *enabled24)
	}
	if enabled5 != nil {
		setCheckbox(values, form, "macmode_11a", *enabled5)
	}

	if err := c.submitForm(ctx, page, form, values, ""); err != nil {
		return nil, err
	}
	return c.ReadMacFiltering(ctx)
}

func (c *Client) AddMAC(ctx context.Context, mac string) error {
	normalized := NormalizeMAC(mac)
	if !IsMACAddress(normalized) {
		return fmt.Errorf("invalid MAC address: %s", mac)
	}

	page, err := c.takeMacRegPage(ctx)
	if err != nil {
		return err
	}
	form, err := findForm(page.Doc, func(form *Form) bool {
		return form.HasControl("maclist") && form.HasControl("ADD")
	})
	if err != nil {
		title := strings.TrimSpace(page.Doc.Find("title").First().Text())
		var controls []string
		page.Doc.Find("form").Each(func(_ int, s *goquery.Selection) {
			s.Find("input, button, select, textarea").Each(func(_ int, el *goquery.Selection) {
				name, _ := el.Attr("name")
				typ, _ := el.Attr("type")
				controls = append(controls, fmt.Sprintf("%s[%s]", name, typ))
			})
		})
		return fmt.Errorf("MAC add form not found (url=%s title=%q controls=%v): %w", page.URL, title, controls, err)
	}

	values := form.Values("ADD")
	values.Set("maclist", normalized)
	result, err := c.submitFormWithPage(ctx, page, form, values, "ADD")
	if err != nil {
		return err
	}
	// DEBUG: dump response page for inspection
	if html, err2 := result.Doc.Html(); err2 == nil {
		_ = os.WriteFile("/tmp/mac_add_response.html", []byte(html), 0o644)
	}
	c.macRegPage = result
	return nil
}

func (c *Client) UpdateMAC(ctx context.Context, currentMAC, newMAC string) (*MacFilterState, error) {
	if err := c.UpdateMACEntry(ctx, currentMAC, newMAC); err != nil {
		return nil, err
	}
	return c.ReadMacFiltering(ctx)
}

func (c *Client) UpdateMACEntry(ctx context.Context, currentMAC, newMAC string) error {
	current := NormalizeMAC(currentMAC)
	next := NormalizeMAC(newMAC)
	if !IsMACAddress(current) || !IsMACAddress(next) {
		return errors.New("invalid MAC address")
	}

	entry, err := c.findMACRegistryEntry(ctx, current)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/cgi-bin/cgi?req=frm&frm=mac_reg.html&EDIT%d=1", entry.Index)
	page, err := c.getAuthenticatedPage(ctx, path)
	if err != nil {
		return err
	}

	submitName := fmt.Sprintf("EDIT%d", entry.Index)
	form, err := findForm(page.Doc, func(form *Form) bool {
		return form.HasControl("maclist") && form.HasControl(submitName)
	})
	if err != nil {
		return fmt.Errorf("MAC edit form not found: %w", err)
	}

	values := form.Values(submitName)
	values.Set("maclist", next)
	return c.submitForm(ctx, page, form, values, submitName)
}

func (c *Client) RemoveMAC(ctx context.Context, mac string) error {
	normalized := NormalizeMAC(mac)
	page, err := c.takeMacRegPage(ctx)
	if err != nil {
		return err
	}

	var controlName string
	page.Doc.Find("table.AD_LIST tr").EachWithBreak(func(index int, row *goquery.Selection) bool {
		if index == 0 {
			return true
		}
		if NormalizeMAC(row.Find("td").First().Text()) != normalized {
			return true
		}
		row.Find(`input[type="hidden"][name^="DELETE"]`).EachWithBreak(func(_ int, input *goquery.Selection) bool {
			name, _ := input.Attr("name")
			if deletePattern.MatchString(name) {
				controlName = name
				return false
			}
			return true
		})
		return controlName == ""
	})
	if controlName == "" {
		return fmt.Errorf("MAC entry not found: %s", normalized)
	}

	form, err := findForm(page.Doc, func(form *Form) bool {
		return form.HasControl(controlName)
	})
	if err != nil {
		return fmt.Errorf("MAC delete form not found: %w", err)
	}

	values := form.Values(controlName)
	result, err := c.submitFormWithPage(ctx, page, form, values, controlName)
	if err != nil {
		return err
	}
	c.macRegPage = result
	return nil
}

func (c *Client) ReadDHCPStaticAssignments(ctx context.Context) ([]DHCPAssignment, error) {
	page, err := c.getAuthenticatedPage(ctx, "/cgi-bin/cgi?req=frm&frm=dhcps_lease.html")
	if err != nil {
		return nil, err
	}

	assignments := make([]DHCPAssignment, 0)
	page.Doc.Find("table.AD_LIST tr").Each(func(index int, row *goquery.Selection) {
		if index == 0 {
			return
		}
		cells := row.Find("td")
		if cells.Length() < 5 {
			return
		}

		ipText := strings.TrimSpace(cells.Eq(0).Text())
		currentClient := strings.Contains(ipText, "(*)")
		ip := strings.TrimSpace(strings.ReplaceAll(ipText, "(*)", ""))

		mac := NormalizeMAC(cells.Eq(1).Text())
		if !IsIPv4(ip) || !IsMACAddress(mac) {
			return
		}

		deleteName, _ := row.Find(`input[name^="DEL"]`).Attr("name")
		deleteIndex := parseOptionalInt(delPattern, deleteName)

		var editTag *int
		row.Find(`[onclick*="edittag="]`).EachWithBreak(func(_ int, selection *goquery.Selection) bool {
			onclick, ok := selection.Attr("onclick")
			if !ok {
				return true
			}
			editTag = parseOptionalInt(editTagPattern, onclick)
			return editTag == nil
		})

		assignments = append(assignments, DHCPAssignment{
			IP:            ip,
			MAC:           mac,
			Lease:         strings.TrimSpace(cells.Eq(2).Text()),
			Status:        strings.TrimSpace(cells.Eq(3).Text()),
			DeleteIndex:   deleteIndex,
			EditTag:       editTag,
			CurrentClient: currentClient,
		})
	})

	return assignments, nil
}

func (c *Client) AddDHCPStaticAssignment(ctx context.Context, ip, mac string) ([]DHCPAssignment, error) {
	normalizedMAC := NormalizeMAC(mac)
	if !IsIPv4(ip) || !IsMACAddress(normalizedMAC) {
		return nil, errors.New("invalid IP or MAC address")
	}

	page, err := c.getAuthenticatedPage(ctx, "/cgi-bin/cgi?req=frm&frm=dhcps_lease_edit.html&edittag=-1&EDITTAG_FROM_NEW=1")
	if err != nil {
		return nil, err
	}
	form, err := findForm(page.Doc, func(form *Form) bool {
		return form.HasControl("manip-1") && form.HasControl("manmac-1") && form.HasControl("ADD")
	})
	if err != nil {
		return nil, fmt.Errorf("DHCP add form not found: %w", err)
	}

	values := form.Values("ADD")
	values.Set("manip-1", ip)
	values.Set("manmac-1", normalizedMAC)
	if err := c.submitForm(ctx, page, form, values, "ADD"); err != nil {
		return nil, err
	}
	return c.ReadDHCPStaticAssignments(ctx)
}

func (c *Client) UpdateDHCPStaticAssignment(ctx context.Context, selector string, nextIP, nextMAC string) ([]DHCPAssignment, error) {
	current, err := c.findDHCPAssignment(ctx, selector)
	if err != nil {
		return nil, err
	}
	if current.EditTag == nil {
		return nil, fmt.Errorf("DHCP assignment cannot be edited: %s", selector)
	}

	ip := current.IP
	if nextIP != "" {
		ip = nextIP
	}
	mac := current.MAC
	if nextMAC != "" {
		mac = NormalizeMAC(nextMAC)
	}
	if !IsIPv4(ip) || !IsMACAddress(mac) {
		return nil, errors.New("invalid IP or MAC address")
	}

	path := fmt.Sprintf("/cgi-bin/cgi?req=frm&frm=dhcps_lease_edit.html&edittag=%d", *current.EditTag)
	page, err := c.getAuthenticatedPage(ctx, path)
	if err != nil {
		return nil, err
	}

	fieldIP := fmt.Sprintf("manip%d", *current.EditTag)
	fieldMAC := fmt.Sprintf("manmac%d", *current.EditTag)
	submitName := fmt.Sprintf("DOFIX%d", *current.EditTag)
	form, err := findForm(page.Doc, func(form *Form) bool {
		return form.HasControl(fieldIP) && form.HasControl(fieldMAC) && form.HasControl(submitName)
	})
	if err != nil {
		return nil, fmt.Errorf("DHCP edit form not found: %w", err)
	}

	values := form.Values(submitName)
	values.Set(fieldIP, ip)
	values.Set(fieldMAC, mac)
	if err := c.submitForm(ctx, page, form, values, submitName); err != nil {
		return nil, err
	}
	return c.ReadDHCPStaticAssignments(ctx)
}

func (c *Client) RemoveDHCPStaticAssignment(ctx context.Context, selector string) ([]DHCPAssignment, error) {
	current, err := c.findDHCPAssignment(ctx, selector)
	if err != nil {
		return nil, err
	}
	if current.DeleteIndex == nil {
		return nil, fmt.Errorf("DHCP assignment cannot be removed: %s", selector)
	}

	page, err := c.getAuthenticatedPage(ctx, "/cgi-bin/cgi?req=frm&frm=dhcps_lease.html")
	if err != nil {
		return nil, err
	}

	submitName := fmt.Sprintf("DEL%d", *current.DeleteIndex)
	form, err := findForm(page.Doc, func(form *Form) bool {
		return form.HasControl(submitName)
	})
	if err != nil {
		return nil, fmt.Errorf("DHCP delete form not found: %w", err)
	}

	values := form.Values(submitName)
	if err := c.submitForm(ctx, page, form, values, submitName); err != nil {
		return nil, err
	}
	return c.ReadDHCPStaticAssignments(ctx)
}

func (c *Client) Logout(ctx context.Context) error {
	if !c.loggedIn {
		return nil
	}
	c.loggedIn = false

	page, _, err := c.fetchPage(ctx, loginPath, nil)
	if err != nil {
		return err
	}

	// Look for a logout form (e.g. "force logout" button on the login page)
	form, err := findForm(page.Doc, func(form *Form) bool {
		return form.HasControl("logout") || form.HasControl("LOGOUT")
	})
	if err != nil {
		// No logout form found — session will expire naturally
		return nil
	}

	values := form.Values("")
	return c.submitForm(ctx, page, form, values, "")
}

func (c *Client) getAuthenticatedPage(ctx context.Context, path string) (*Page, error) {
	if err := c.Login(ctx); err != nil {
		return nil, err
	}
	page, _, err := c.fetchPage(ctx, path, nil)
	if err != nil {
		return nil, err
	}
	if isLoginPage(page.Doc) {
		c.loggedIn = false
		return nil, errors.New("session expired")
	}
	return page, nil
}

func (c *Client) fetchPage(ctx context.Context, path string, referer *url.URL) (*Page, string, error) {
	target, err := c.resolveURL(path)
	if err != nil {
		return nil, "", err
	}
	doc, finalURL, html, err := c.requestDocument(ctx, http.MethodGet, target, nil, referer)
	if err != nil {
		return nil, "", err
	}
	return &Page{URL: finalURL, Doc: doc}, html, nil
}

func (c *Client) submitForm(ctx context.Context, page *Page, form *Form, values url.Values, clickedName string) error {
	_, err := c.submitFormWithPage(ctx, page, form, values, clickedName)
	return err
}

func (c *Client) submitFormWithPage(ctx context.Context, page *Page, form *Form, values url.Values, clickedName string) (*Page, error) {
	target, err := page.URL.Parse(form.Action)
	if err != nil {
		return nil, fmt.Errorf("resolve form action: %w", err)
	}
	method := form.Method
	if method == "" {
		method = http.MethodPost
	}
	method = strings.ToUpper(method)

	if clickedName != "" && values.Get(clickedName) == "" {
		if defaultValue, ok := form.DefaultValue(clickedName); ok {
			values.Set(clickedName, defaultValue)
		}
	}

	doc, finalURL, _, err := c.requestDocument(ctx, method, target, values, page.URL)
	if err != nil {
		return nil, err
	}
	return &Page{URL: finalURL, Doc: doc}, nil
}

// takeMacRegPage returns the cached POST-response page from the last mac_reg.html
// operation, or fetches a fresh copy if no cache is available. Using the POST
// response avoids a round-trip that puts the router into a "pending" state where
// the ADD form is no longer visible.
func (c *Client) takeMacRegPage(ctx context.Context) (*Page, error) {
	if page := c.macRegPage; page != nil {
		c.macRegPage = nil
		return page, nil
	}
	return c.getAuthenticatedPage(ctx, "/cgi-bin/cgi?req=frm&frm=mac_reg.html")
}

func (c *Client) requestDocument(ctx context.Context, method string, target *url.URL, values url.Values, referer *url.URL) (*goquery.Document, *url.URL, string, error) {
	req, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
	if err != nil {
		return nil, nil, "", err
	}
	resp, err := c.doRequest(req, values, referer)
	if err != nil {
		return nil, nil, "", err
	}

	html, err := decodeHTML(resp.Header.Get("Content-Type"), resp.Body)
	if err != nil {
		return nil, nil, "", err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, nil, "", err
	}

	return doc, resp.RequestURL, html, nil
}

func (c *Client) resolveURL(path string) (*url.URL, error) {
	target, err := url.Parse(path)
	if err != nil {
		return nil, err
	}
	return c.baseURL.ResolveReference(target), nil
}

func (c *Client) findMACRegistryEntry(ctx context.Context, mac string) (*MacRegistryEntry, error) {
	page, err := c.getAuthenticatedPage(ctx, "/cgi-bin/cgi?req=frm&frm=mac_reg.html")
	if err != nil {
		return nil, err
	}
	var result *MacRegistryEntry
	page.Doc.Find("table.AD_LIST tr").EachWithBreak(func(index int, row *goquery.Selection) bool {
		if index == 0 {
			return true
		}
		rowMAC := NormalizeMAC(row.Find("td").First().Text())
		if rowMAC != mac {
			return true
		}
		row.Find(`input[type="hidden"][name^="DELETE"]`).EachWithBreak(func(_ int, input *goquery.Selection) bool {
			name, _ := input.Attr("name")
			if indexValue := parseOptionalInt(deletePattern, name); indexValue != nil {
				result = &MacRegistryEntry{MAC: rowMAC, Index: *indexValue}
				return false
			}
			return true
		})
		return result == nil
	})
	if result == nil {
		return nil, fmt.Errorf("MAC entry not found: %s", mac)
	}
	return result, nil
}

func (c *Client) findDHCPAssignment(ctx context.Context, selector string) (*DHCPAssignment, error) {
	normalizedSelector := NormalizeMAC(selector)
	if !IsMACAddress(normalizedSelector) {
		normalizedSelector = strings.TrimSpace(selector)
	}

	assignments, err := c.ReadDHCPStaticAssignments(ctx)
	if err != nil {
		return nil, err
	}

	for _, assignment := range assignments {
		if assignment.IP == normalizedSelector || assignment.MAC == normalizedSelector {
			copy := assignment
			return &copy, nil
		}
	}
	return nil, fmt.Errorf("DHCP static assignment not found: %s", selector)
}

func NormalizeMAC(mac string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(mac), "-", ":"))
}

func IsMACAddress(value string) bool {
	return macPattern.MatchString(strings.TrimSpace(value))
}

func IsIPv4(value string) bool {
	parsed := net.ParseIP(strings.TrimSpace(value))
	return parsed != nil && parsed.To4() != nil
}

func isAnotherUserLoggedIn(doc *goquery.Document) bool {
	return isLoginPage(doc) &&
		(doc.Find(`input[name="logout"]`).Length() > 0 || doc.Find(`input[name="LOGOUT"]`).Length() > 0)
}

func isLoginPage(doc *goquery.Document) bool {
	title := strings.TrimSpace(doc.Find("title").First().Text())
	return strings.EqualFold(title, "LOGIN") ||
		(doc.Find("#authform").Length() > 0 && doc.Find(`input[name="airstation_pass"]`).Length() > 0)
}

func setCheckbox(values url.Values, form *Form, name string, checked bool) {
	if checked {
		defaultValue, ok := form.DefaultValue(name)
		if !ok {
			defaultValue = "on"
		}
		values.Set(name, defaultValue)
		return
	}
	values.Del(name)
}

func parseOptionalInt(pattern *regexp.Regexp, input string) *int {
	match := pattern.FindStringSubmatch(input)
	if len(match) < 2 {
		return nil
	}
	value, err := strconv.Atoi(match[1])
	if err != nil {
		return nil
	}
	return &value
}

func extractLoginKey(html string) (modulus, exponent string, err error) {
	modulusMatch := modulusPattern.FindStringSubmatch(html)
	exponentMatch := exponentPattern.FindStringSubmatch(html)
	if len(modulusMatch) < 2 || len(exponentMatch) < 2 {
		return "", "", errors.New("login RSA key not found")
	}
	return modulusMatch[1], exponentMatch[1], nil
}

func buildEncryptedPayload(name, value string) string {
	return encodeURIComponent(name) + "=" + encodeURIComponent(value)
}

func encryptPassword(modulusHex, exponentText, plaintext string) (string, error) {
	modulus := new(big.Int)
	if _, ok := modulus.SetString(modulusHex, 16); !ok {
		return "", errors.New("invalid RSA modulus")
	}
	exponent, err := strconv.Atoi(exponentText)
	if err != nil {
		return "", fmt.Errorf("parse RSA exponent: %w", err)
	}

	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, &rsa.PublicKey{
		N: modulus,
		E: exponent,
	}, []byte(plaintext))
	if err != nil {
		return "", err
	}

	return wrap64(base64.StdEncoding.EncodeToString(ciphertext)), nil
}

func wrap64(input string) string {
	if len(input) <= 64 {
		return input
	}

	var builder strings.Builder
	for start := 0; start < len(input); start += 64 {
		end := min(start+64, len(input))
		if start > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(input[start:end])
	}
	return builder.String()
}

func encodeURIComponent(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if isURIComponentSafe(r) {
			builder.WriteRune(r)
			continue
		}
		for _, b := range []byte(string(r)) {
			fmt.Fprintf(&builder, "%%%02X", b)
		}
	}
	return builder.String()
}

func isURIComponentSafe(r rune) bool {
	switch {
	case r >= 'A' && r <= 'Z':
		return true
	case r >= 'a' && r <= 'z':
		return true
	case r >= '0' && r <= '9':
		return true
	}
	switch r {
	case '-', '_', '.', '!', '~', '*', '\'', '(', ')':
		return true
	default:
		return false
	}
}
