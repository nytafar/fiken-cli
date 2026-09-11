// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Binary download (issue #23). Every generated request path decodes the
// response as JSON, so the one thing an inbox document or a purchase
// attachment is actually for — the PDF behind its `documentUrl` /
// `downloadUrl` — was unreachable without pulling the access token out of
// the config file and curling the URL by hand. DownloadFile is the missing
// transport: the client's own auth material and HTTP client, the bytes
// streamed to an io.Writer instead of buffered into a json.RawMessage.

package client

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// DownloadedFile describes what came back from a DownloadFile call. Filename
// is the server's suggestion from Content-Disposition and is empty when the
// response carried none; callers that already know a filename from the
// resource metadata should prefer theirs.
type DownloadedFile struct {
	Bytes       int64
	Filename    string
	ContentType string
}

// DownloadFile GETs rawURL with the client's auth and copies the response
// body to w. rawURL may be absolute (as Fiken returns it in documentUrl /
// downloadUrl) or a path relative to the client's base URL.
//
// The Authorization header is attached only when the URL host matches the
// client's configured base URL host: Fiken's document URLs live on the API
// host, and a redirect-style URL pointing anywhere else must not be handed
// the bearer token. A foreign host is still fetched, unauthenticated, so a
// pre-signed URL keeps working.
func (c *Client) DownloadFile(ctx context.Context, rawURL string, w io.Writer) (DownloadedFile, error) {
	var out DownloadedFile

	u, err := c.resolveDownloadURL(rawURL)
	if err != nil {
		return out, err
	}

	authHeader, err := c.authHeader(ctx)
	if err != nil {
		return out, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return out, fmt.Errorf("creating request: %w", err)
	}
	if authHeader != "" && sameHost(u, c.BaseURL) {
		req.Header.Set("Authorization", authHeader)
	}
	if c.Config != nil {
		for k, v := range c.Config.Headers {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("Accept", "*/*")
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "fiken-cli/2.0.0")
	}

	if c.limiter != nil {
		c.limiter.Wait()
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return out, fmt.Errorf("GET %s: %w", c.displayURL(u.String(), authHeader), c.maskError(err, authHeader))
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return out, &APIError{
			Method:     http.MethodGet,
			Path:       c.displayURL(u.String(), authHeader),
			StatusCode: resp.StatusCode,
			Body:       c.maskCredentialText(truncateBody(body), authHeader),
		}
	}
	if c.limiter != nil {
		c.limiter.OnSuccess()
	}

	out.ContentType = resp.Header.Get("Content-Type")
	out.Filename = filenameFromResponse(resp, u)

	n, err := io.Copy(w, resp.Body)
	out.Bytes = n
	if err != nil {
		return out, fmt.Errorf("writing downloaded body: %w", err)
	}
	return out, nil
}

// resolveDownloadURL accepts both an absolute URL and a base-relative path.
func (c *Client) resolveDownloadURL(rawURL string) (*url.URL, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil, fmt.Errorf("empty download URL")
	}
	if strings.HasPrefix(trimmed, "/") {
		trimmed = strings.TrimRight(c.BaseURL, "/") + trimmed
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("parsing download URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported download URL scheme %q", u.Scheme)
	}
	return u, nil
}

func sameHost(u *url.URL, baseURL string) bool {
	base, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, base.Host)
}

// filenameFromResponse prefers the server's Content-Disposition filename and
// falls back to the last path segment of the URL. Returns "" when neither
// yields a usable name; the caller decides the fallback.
//
// Either fallback can produce an extensionless name -- Fiken's document URLs
// end in the numeric document id, so a response with no Content-Disposition
// resolves to "42" for a file the Content-Type says is a PDF. The extension
// is put back from the content type, because the name is what the CLI writes
// to disk and a "bilag/42" that no viewer will open is not a downloaded PDF.
func filenameFromResponse(resp *http.Response, u *url.URL) string {
	contentType := resp.Header.Get("Content-Type")
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			if name := SanitizeFilename(params["filename"]); name != "" {
				return EnsureFilenameExtension(name, contentType)
			}
		}
	}
	return EnsureFilenameExtension(SanitizeFilename(path.Base(u.Path)), contentType)
}

// EnsureFilenameExtension appends the extension implied by contentType when
// name carries none. An empty name is left empty (the caller's fallback
// decides), and an unknown content type adds nothing rather than guessing.
func EnsureFilenameExtension(name, contentType string) string {
	if name == "" || path.Ext(name) != "" {
		return name
	}
	return name + extensionForContentType(contentType)
}

// extensionForContentType maps a response Content-Type to a file extension.
// The four types Fiken actually serves for documents are mapped explicitly so
// the answer is stable (mime.ExtensionsByType is seeded from the system
// mime.types and would hand back ".jpe" or ".pdf" depending on the host);
// anything else falls back to the system table, then to no extension.
func extensionForContentType(contentType string) string {
	mediaType := strings.ToLower(strings.TrimSpace(contentType))
	if parsed, _, err := mime.ParseMediaType(contentType); err == nil {
		mediaType = strings.ToLower(strings.TrimSpace(parsed))
	}
	switch mediaType {
	case "":
		return ""
	case "application/pdf":
		return ".pdf"
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	}
	exts, err := mime.ExtensionsByType(mediaType)
	if err != nil || len(exts) == 0 {
		return ""
	}
	// ExtensionsByType returns a sorted slice, so this is deterministic.
	return exts[0]
}

// SanitizeFilename reduces a server- or metadata-supplied name to a
// single path element. A name is attacker-influenced data; "../../etc/x" must
// never escape the directory the caller chose.
func SanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	switch name {
	case ".", "..", "/", "":
		return ""
	}
	return name
}
