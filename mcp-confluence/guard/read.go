package guard

import (
	"net/http"
	"strconv"
)

const (
	startParam   = "start"
	limitParam   = "limit"
	listLimitMax = 100
	depthParam   = "depth"
	depthAll     = "all"
	versionParam = "version"
	apiParam     = "api"
	apiV2        = "v2"
)

var (
	commentExpand        = []string{"body.storage", "history", "ancestors"}
	attachmentListExpand = []string{"version"}

	requiredStartIndex = queryParam{rule: isNonNegativeNumber, required: true}
	requiredListLimit  = queryParam{rule: limitWithin(listLimitMax), required: true}

	getContentQuery      = queryRules{expandParam: {rule: expandDistinctWithin(contentExpand), required: true}}
	listQuery            = queryRules{startParam: requiredStartIndex, limitParam: requiredListLimit}
	listAttachmentsQuery = queryRules{
		expandParam: {rule: expandDistinctWithin(attachmentListExpand)},
		startParam:  requiredStartIndex,
		limitParam:  requiredListLimit,
	}
	listCommentsQuery = queryRules{
		expandParam: {rule: expandDistinctWithin(commentExpand), required: true},
		depthParam:  {rule: exactly(depthAll), required: true},
		startParam:  requiredStartIndex,
		limitParam:  requiredListLimit,
	}
	downloadQuery = queryRules{
		versionParam: {rule: isPositiveNumber, required: true},
		apiParam:     {rule: exactly(apiV2), required: true},
	}
)

func isNonNegativeNumber(value string) bool {
	return value == "0" || isPositiveNumber(value)
}

func limitWithin(maxLimit int) queryRule {
	return func(value string) bool {
		if !isPositiveNumber(value) {
			return false
		}
		limit, err := strconv.Atoi(value)
		return err == nil && limit <= maxLimit
	}
}

func exactly(expected string) queryRule {
	return func(value string) bool { return value == expected }
}

func (t *Transport) authorizeGetContent(req *http.Request, contentID string) (*http.Request, error) {
	return t.authorizeReadInZone(req, contentID, getContentQuery)
}

func (t *Transport) authorizeListChildren(req *http.Request, contentID string) (*http.Request, error) {
	return t.authorizeReadInZone(req, contentID, listQuery)
}

func (t *Transport) authorizeListComments(req *http.Request, contentID string) (*http.Request, error) {
	return t.authorizeReadInZone(req, contentID, listCommentsQuery)
}

func (t *Transport) authorizeListAttachments(req *http.Request, contentID string) (*http.Request, error) {
	return t.authorizeReadInZone(req, contentID, listAttachmentsQuery)
}

func (t *Transport) authorizeDownload(req *http.Request, pageID string) (*http.Request, error) {
	return t.authorizeReadInZone(req, pageID, downloadQuery)
}

func (t *Transport) authorizeReadInZone(req *http.Request, contentID string, rules queryRules) (*http.Request, error) {
	if err := checkQuery(req.URL, rules); err != nil {
		return nil, err
	}
	if err := checkReadOnlyRequest(req); err != nil {
		return nil, err
	}
	if _, err := t.requirePageInZone(req, contentID, t.readZone); err != nil {
		return nil, err
	}
	return req, nil
}
