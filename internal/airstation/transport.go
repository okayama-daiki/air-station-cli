package airstation

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type rawResponse struct {
	Header     textproto.MIMEHeader
	Body       []byte
	RequestURL *url.URL
	StatusCode int
}

func (c *Client) doRequest(req *http.Request, values url.Values, referer *url.URL) (*rawResponse, error) {
	return c.doRequestWithRedirects(req.Context(), req.Method, req.URL, values, referer, 0)
}

func (c *Client) doRequestWithRedirects(ctx context.Context, method string, target *url.URL, values url.Values, referer *url.URL, redirects int) (*rawResponse, error) {
	if redirects > 10 {
		return nil, fmt.Errorf("too many redirects")
	}

	resp, err := c.roundTrip(ctx, method, target, values, referer)
	if err != nil {
		return nil, err
	}

	location := resp.Header.Get("Location")
	if resp.StatusCode >= 300 && resp.StatusCode < 400 && location != "" {
		nextURL, err := target.Parse(location)
		if err != nil {
			return nil, err
		}
		nextMethod := method
		var nextValues url.Values
		if resp.StatusCode == http.StatusSeeOther || ((resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently) && method == http.MethodPost) {
			nextMethod = http.MethodGet
		}
		if nextMethod == http.MethodPost {
			nextValues = values
		}
		return c.doRequestWithRedirects(ctx, nextMethod, nextURL, nextValues, target, redirects+1)
	}

	return resp, nil
}

func (c *Client) roundTrip(ctx context.Context, method string, target *url.URL, values url.Values, referer *url.URL) (*rawResponse, error) {
	conn, err := c.dial(ctx, target)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if c.timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(c.timeout))
	}

	body := ""
	if values != nil {
		if method == http.MethodGet {
			q := target.Query()
			for key, vals := range values {
				for _, v := range vals {
					q.Add(key, v)
				}
			}
			target.RawQuery = q.Encode()
		} else {
			body = values.Encode()
		}
	}

	var request bytes.Buffer
	fmt.Fprintf(&request, "%s %s HTTP/1.0\r\n", method, target.RequestURI())
	fmt.Fprintf(&request, "Host: %s\r\n", target.Host)
	fmt.Fprintf(&request, "User-Agent: %s\r\n", c.userAgent)
	fmt.Fprintf(&request, "Accept: text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8\r\n")
	fmt.Fprintf(&request, "Connection: close\r\n")
	if referer != nil {
		fmt.Fprintf(&request, "Referer: %s\r\n", referer.String())
	}
	if cookies := c.cookiesFor(target); cookies != "" {
		fmt.Fprintf(&request, "Cookie: %s\r\n", cookies)
	}
	if body != "" {
		fmt.Fprintf(&request, "Content-Type: application/x-www-form-urlencoded\r\n")
		fmt.Fprintf(&request, "Content-Length: %d\r\n", len(body))
	}
	request.WriteString("\r\n")
	request.WriteString(body)

	if c.verbose {
		fmt.Fprintf(os.Stderr, "> %s %s\n", method, target.RequestURI())
	}

	if _, err := conn.Write(request.Bytes()); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	statusCode, err := parseStatusCode(statusLine)
	if err != nil {
		return nil, err
	}

	if c.verbose {
		fmt.Fprintf(os.Stderr, "< %s", strings.TrimRight(statusLine, "\r\n"))
		fmt.Fprintln(os.Stderr)
	}

	headers := make(textproto.MIMEHeader)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			break
		}
		if !strings.Contains(trimmed, ":") {
			continue
		}
		name, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		headers.Add(textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name)), strings.TrimSpace(value))
	}

	bodyBytes, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	c.storeCookies(target, headers)

	return &rawResponse{
		Header:     headers,
		Body:       bodyBytes,
		RequestURL: target,
		StatusCode: statusCode,
	}, nil
}

func (c *Client) dial(ctx context.Context, target *url.URL) (net.Conn, error) {
	host := target.Host
	if !strings.Contains(host, ":") {
		switch target.Scheme {
		case "https":
			host += ":443"
		default:
			host += ":80"
		}
	}

	dialer := &net.Dialer{}
	if c.timeout > 0 {
		dialer.Timeout = c.timeout
	}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, err
	}
	if target.Scheme != "https" {
		return conn, nil
	}

	serverName := target.Hostname()
	tlsConn := tls.Client(conn, &tls.Config{ServerName: serverName})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return tlsConn, nil
}

func (c *Client) cookiesFor(target *url.URL) string {
	cookies := c.jar.Cookies(target)
	if len(cookies) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(parts, "; ")
}

func (c *Client) storeCookies(target *url.URL, headers textproto.MIMEHeader) {
	rawCookies := headers["Set-Cookie"]
	if len(rawCookies) == 0 {
		return
	}
	cookies := make([]*http.Cookie, 0, len(rawCookies))
	for _, rawCookie := range rawCookies {
		cookie, err := http.ParseSetCookie(rawCookie)
		if err != nil {
			continue
		}
		cookies = append(cookies, cookie)
	}
	if len(cookies) > 0 {
		c.jar.SetCookies(target, cookies)
	}
}

func parseStatusCode(statusLine string) (int, error) {
	fields := strings.Fields(statusLine)
	if len(fields) < 2 {
		return 0, fmt.Errorf("invalid status line: %q", strings.TrimSpace(statusLine))
	}
	code, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, fmt.Errorf("invalid status code: %w", err)
	}
	return code, nil
}
