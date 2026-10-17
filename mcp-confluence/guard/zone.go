package guard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
)

const (
	zoneCheckExpand = "ancestors,space,container"
	typePage        = "page"
	typeAttachment  = "attachment"
	statusCurrent   = "current"
)

var (
	ErrOutOfZone       = errors.New("guard: content is outside the allowed subtrees or does not exist")
	ErrZoneCheckFailed = errors.New("guard: zone check failed, the request was not sent")
	ErrTokenRejected   = errors.New("guard: zone check was not authorized")

	errContentNotVisible = errors.New("content is not visible to the token")
)

type zone map[string]struct{}

func newZone(roots []string) zone {
	allowed := make(zone, len(roots))
	for _, root := range roots {
		allowed[root] = struct{}{}
	}
	return allowed
}

func (z zone) containsPage(info contentInfo) bool {
	if info.Type != typePage || info.Status != statusCurrent {
		return false
	}
	if _, isRoot := z[info.ID]; isRoot {
		return true
	}
	return slices.ContainsFunc(info.AncestorIDs, func(ancestorID string) bool {
		_, isRoot := z[ancestorID]
		return isRoot
	})
}

type responseIDMatcher func(responseID, requestedID string) bool

func samePageID(responseID, requestedID string) bool {
	return responseID == requestedID
}

func sameAttachmentID(responseID, requestedID string) bool {
	contentID, ok := AttachmentContentID(responseID)
	return ok && contentID == requestedID
}

type contentInfo struct {
	ID            string
	Type          string
	Status        string
	SpaceKey      string
	AncestorIDs   []string
	ContainerID   string
	ContainerType string
}

func (t *Transport) requirePageInZone(req *http.Request, contentID string, allowed zone) (contentInfo, error) {
	info, err := t.fetchContentInfo(req, contentID, samePageID)
	switch {
	case errors.Is(err, errContentNotVisible):
		return contentInfo{}, outOfZone(contentID)
	case err != nil:
		return contentInfo{}, err
	case !allowed.containsPage(info):
		return contentInfo{}, outOfZone(contentID)
	default:
		return info, nil
	}
}

func outOfZone(contentID string) error {
	return fmt.Errorf("%w: %s", ErrOutOfZone, contentID)
}

func (t *Transport) fetchContentInfo(req *http.Request, contentID string, isRequestedID responseIDMatcher) (contentInfo, error) {
	zoneCheckURL := url.URL{Scheme: t.scheme, Host: t.host, Path: t.contentPrefix + "/" + contentID, RawQuery: expandParam + "=" + zoneCheckExpand}
	zoneCheckRequest, err := http.NewRequestWithContext(req.Context(), http.MethodGet, zoneCheckURL.String(), nil)
	if err != nil {
		return contentInfo{}, fmt.Errorf("%w: %w", ErrZoneCheckFailed, err)
	}
	zoneCheckRequest.Header.Set(authorizationHeader, req.Header.Get(authorizationHeader))
	zoneCheckRequest.Header.Set("Accept", "application/json")
	response, err := t.nextTransport.RoundTrip(zoneCheckRequest)
	if err != nil {
		return contentInfo{}, fmt.Errorf("%w: request for %s failed", ErrZoneCheckFailed, contentID)
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusOK:
		return decodeContentInfo(response.Body, contentID, isRequestedID)
	case http.StatusUnauthorized:
		return contentInfo{}, ErrTokenRejected
	case http.StatusForbidden, http.StatusNotFound:
		return contentInfo{}, errContentNotVisible
	default:
		return contentInfo{}, fmt.Errorf("%w: status %d", ErrZoneCheckFailed, response.StatusCode)
	}
}

type containerID string

func (id *containerID) UnmarshalJSON(data []byte) error {
	var text string
	if json.Unmarshal(data, &text) == nil {
		*id = containerID(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	*id = containerID(number)
	return nil
}

func decodeContentInfo(body io.Reader, contentID string, isRequestedID responseIDMatcher) (contentInfo, error) {
	data, err := readLimited(body, MaxBodyBytes)
	if err != nil {
		return contentInfo{}, fmt.Errorf("%w: response for %s is unreadable or too large", ErrZoneCheckFailed, contentID)
	}
	var parsed struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Status string `json:"status"`
		Space  struct {
			Key string `json:"key"`
		} `json:"space"`
		Ancestors []struct {
			ID string `json:"id"`
		} `json:"ancestors"`
		Container struct {
			ID   containerID `json:"id"`
			Type string      `json:"type"`
		} `json:"container"`
	}
	if json.Unmarshal(data, &parsed) != nil {
		return contentInfo{}, fmt.Errorf("%w: response for %s is not content JSON", ErrZoneCheckFailed, contentID)
	}
	if !isRequestedID(parsed.ID, contentID) {
		return contentInfo{}, fmt.Errorf("%w: response id differs from %s", ErrZoneCheckFailed, contentID)
	}
	info := contentInfo{ID: parsed.ID, Type: parsed.Type, Status: parsed.Status, SpaceKey: parsed.Space.Key, ContainerID: string(parsed.Container.ID), ContainerType: parsed.Container.Type}
	for _, ancestor := range parsed.Ancestors {
		info.AncestorIDs = append(info.AncestorIDs, ancestor.ID)
	}
	return info, nil
}
