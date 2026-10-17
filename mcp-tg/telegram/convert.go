package telegram

import (
	"fmt"
	"time"

	"github.com/gotd/td/tg"

	"scoped-mcp/mcp-tg/config"
)

type Chat struct {
	ID    int64           `json:"id"`
	Title string          `json:"title"`
	Type  config.ChatType `json:"type"`
}

type Topic struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Closed bool   `json:"closed"`
}

type Message struct {
	ID            int       `json:"id"`
	Date          time.Time `json:"date"`
	From          *Author   `json:"from,omitempty"`
	Text          string    `json:"text,omitempty"`
	ReplyToID     int       `json:"reply_to_id,omitempty"`
	TopicID       int       `json:"topic_id,omitempty"`
	ForwardedFrom string    `json:"forwarded_from,omitempty"`
	Media         string    `json:"media,omitempty"`
	Outgoing      bool      `json:"outgoing,omitempty"`
}

type Page struct {
	Messages    []Message
	RawCount    int
	OldestRawID int
}

func pageFromResult(result tg.MessagesMessagesClass) Page {
	modified, ok := result.AsModified()
	if !ok {
		return Page{Messages: []Message{}}
	}
	lookup := newNames(modified.GetUsers(), modified.GetChats())
	raw := modified.GetMessages()
	page := Page{Messages: make([]Message, 0, len(raw)), RawCount: len(raw)}
	for _, item := range raw {
		if page.OldestRawID == 0 || item.GetID() < page.OldestRawID {
			page.OldestRawID = item.GetID()
		}
		message, ok := item.(*tg.Message)
		if !ok {
			continue
		}
		page.Messages = append(page.Messages, convertMessage(message, lookup))
	}
	return page
}

func convertMessage(m *tg.Message, lookup names) Message {
	converted := Message{
		ID:       m.ID,
		Date:     time.Unix(int64(m.Date), 0).UTC(),
		Text:     m.Message,
		Outgoing: m.Out,
	}
	if author, ok := lookup.author(senderPeer(m)); ok {
		converted.From = &author
	}
	converted.ReplyToID, converted.TopicID = replyAndTopic(m.ReplyTo)
	if !m.FwdFrom.Zero() {
		converted.ForwardedFrom = forwardSource(m.FwdFrom, lookup)
	}
	if m.Media != nil {
		converted.Media = mediaLabel(m.Media)
	}
	return converted
}

func senderPeer(m *tg.Message) tg.PeerClass {
	if m.FromID != nil {
		return m.FromID
	}
	if m.Out && !m.Post {
		return nil
	}
	return m.PeerID
}

func replyAndTopic(header tg.MessageReplyHeaderClass) (replyToID, topicID int) {
	reply, ok := header.(*tg.MessageReplyHeader)
	if !ok {
		return 0, 0
	}
	msgID, _ := reply.GetReplyToMsgID()
	if !reply.ForumTopic {
		return msgID, 0
	}
	if topID, ok := reply.GetReplyToTopID(); ok {
		return msgID, topID
	}
	topicRootID := msgID
	return 0, topicRootID
}

func forwardSource(header tg.MessageFwdHeader, lookup names) string {
	from, _ := header.GetFromID()
	if title, ok := lookup.title(from); ok {
		return title
	}
	if name, ok := header.GetFromName(); ok && name != "" {
		return name
	}
	return peerLabel(from)
}

func peerLabel(peer tg.PeerClass) string {
	switch p := peer.(type) {
	case *tg.PeerUser:
		return fmt.Sprintf("user %d", p.UserID)
	case *tg.PeerChat:
		return fmt.Sprintf("chat %d", p.ChatID)
	case *tg.PeerChannel:
		return fmt.Sprintf("channel %d", p.ChannelID)
	default:
		return ""
	}
}

func mediaLabel(media tg.MessageMediaClass) string {
	switch m := media.(type) {
	case *tg.MessageMediaPhoto:
		return "photo"
	case *tg.MessageMediaDocument:
		return documentLabel(m)
	case *tg.MessageMediaWebPage:
		return "webpage"
	case *tg.MessageMediaPoll:
		return "poll"
	case *tg.MessageMediaContact:
		return "contact"
	case *tg.MessageMediaGeo, *tg.MessageMediaGeoLive, *tg.MessageMediaVenue:
		return "location"
	default:
		return media.TypeName()
	}
}

func documentLabel(media *tg.MessageMediaDocument) string {
	if media.Voice {
		return "voice"
	}
	if media.Round {
		return "video_note"
	}
	kind := "document"
	if media.Video {
		kind = "video"
	}
	document, ok := media.Document.(*tg.Document)
	if !ok {
		return kind
	}
	if hasSticker(document) {
		return "sticker"
	}
	if name, ok := fileName(document); ok {
		return kind + ": " + name
	}
	return kind + ": " + document.MimeType
}

func hasSticker(document *tg.Document) bool {
	for _, attribute := range document.Attributes {
		if _, ok := attribute.(*tg.DocumentAttributeSticker); ok {
			return true
		}
	}
	return false
}

func fileName(document *tg.Document) (string, bool) {
	for _, attribute := range document.Attributes {
		if named, ok := attribute.(*tg.DocumentAttributeFilename); ok {
			return named.FileName, true
		}
	}
	return "", false
}

func topicsFromResult(raw []tg.ForumTopicClass) []Topic {
	topics := make([]Topic, 0, len(raw))
	for _, item := range raw {
		topic, ok := item.(*tg.ForumTopic)
		if !ok {
			continue
		}
		topics = append(topics, Topic{ID: topic.ID, Title: topic.Title, Closed: topic.Closed})
	}
	return topics
}
