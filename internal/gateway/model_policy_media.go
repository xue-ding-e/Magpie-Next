package gateway

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"strings"

	"github.com/yetone/magpie/internal/provider"
)

// requireAllowedMediaRequest checks the final model carried by media and
// retrieval posts, whose senders do not go through the conversation path.
// It reads the existing plain bytes, never the outgoing request stream.
func requireAllowedMediaRequest(endpoint, ctype string, body []byte) error {
	if provider.RequireAllowedModel("") == nil {
		return nil // no allowlist: preserve the vendor's existing request
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return errors.New("outbound model requests are blocked: the media URL could not be read")
	}
	if _, model, ok := strings.Cut(u.Path, "/models/"); ok {
		if i := strings.LastIndexByte(model, ':'); i >= 0 {
			return provider.RequireAllowedModel(model[:i])
		}
	}
	mt, params, err := mime.ParseMediaType(ctype)
	if err != nil || mt != "multipart/form-data" {
		return provider.RequireAllowedModel(modelOf(body))
	}
	r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	found := false
	for {
		part, err := r.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("outbound model requests are blocked: the media form could not be read")
		}
		if part.FormName() != "model" {
			continue
		}
		if part.FileName() != "" {
			return errors.New("outbound model requests are blocked: the media form names its model as a file")
		}
		model, err := io.ReadAll(io.LimitReader(part, (1<<20)+1))
		if err != nil || len(model) > 1<<20 {
			return errors.New("outbound model requests are blocked: the media form's model could not be read")
		}
		if err := provider.RequireAllowedModel(string(model)); err != nil {
			return err
		}
		found = true
	}
	if !found {
		return provider.RequireAllowedModel("")
	}
	return nil
}
