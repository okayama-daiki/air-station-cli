package airstation

import (
	"bytes"
	"io"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/transform"
)

func decodeHTML(contentType string, body []byte) (string, error) {
	encoding, _, _ := charset.DetermineEncoding(body, contentType)
	reader := transform.NewReader(bytes.NewReader(body), encoding.NewDecoder())
	bytes, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
