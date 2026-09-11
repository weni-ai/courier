package facebookapp

import (
	"context"
	"net/http"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/utils"
	"github.com/pkg/errors"
)

type publicMediaStore interface {
	PutMedia(ctx context.Context, channel courier.Channel, filename, contentType string, contents []byte) (string, error)
}

func (h *handler) rewriteWebPImageURL(msg courier.Msg, attURL string) (string, error) {
	store, ok := h.Backend().(publicMediaStore)
	if !ok {
		store = nil
	}

	get := func(mediaURL string) ([]byte, error) {
		req, err := http.NewRequest(http.MethodGet, mediaURL, nil)
		if err != nil {
			return nil, errors.Wrap(err, "error building media request")
		}

		rr, err := utils.MakeHTTPRequest(req)
		if err != nil {
			return nil, err
		}

		return rr.Body, nil
	}

	put := func(filename, contentType string, contents []byte) (string, error) {
		if store == nil {
			return "", errors.New("media store not available")
		}

		return store.PutMedia(context.Background(), msg.Channel(), filename, contentType, contents)
	}

	return handlers.RewriteWebPImageURL(attURL, get, put)
}
