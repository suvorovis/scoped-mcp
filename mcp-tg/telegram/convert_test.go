package telegram

import (
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
)

func TestPageFromResult(t *testing.T) {
	users := []tg.UserClass{&tg.User{ID: 777, FirstName: "Ivan", LastName: "Petrov"}}
	chats := []tg.ChatClass{&tg.Channel{ID: 300, Title: "Team oncall"}, &tg.Channel{ID: 400, Title: "Team alerts"}}
	channelPeer := &tg.PeerChannel{ChannelID: 300}
	date := 1760432000
	wantDate := time.Unix(int64(date), 0).UTC()

	tests := []struct {
		name   string
		result tg.MessagesMessagesClass
		want   Page
	}{
		{
			name: "текст от пользователя",
			result: &tg.MessagesChannelMessages{Users: users, Chats: chats, Messages: []tg.MessageClass{
				&tg.Message{ID: 1, Date: date, PeerID: channelPeer, FromID: &tg.PeerUser{UserID: 777}, Message: "релиз откатили"},
			}},
			want: Page{Messages: []Message{{ID: 1, Date: wantDate, From: &Author{ID: 777, Name: "Ivan Petrov"}, Text: "релиз откатили"}}, RawCount: 1, OldestRawID: 1},
		},
		{
			name: "пост канала без from_id",
			result: &tg.MessagesChannelMessages{Chats: chats, Messages: []tg.MessageClass{
				&tg.Message{ID: 2, Date: date, Post: true, PeerID: channelPeer, Message: "пост"},
			}},
			want: Page{Messages: []Message{{ID: 2, Date: wantDate, From: &Author{ID: 300, Name: "Team oncall"}, Text: "пост"}}, RawCount: 1, OldestRawID: 2},
		},
		{
			name: "ответ в обычном чате",
			result: &tg.MessagesMessages{Users: users, Messages: []tg.MessageClass{
				&tg.Message{
					ID: 3, Date: date, PeerID: &tg.PeerChat{ChatID: 200}, FromID: &tg.PeerUser{UserID: 777}, Message: "ok",
					ReplyTo: replyTo(4509),
				},
			}},
			want: Page{Messages: []Message{{ID: 3, Date: wantDate, From: &Author{ID: 777, Name: "Ivan Petrov"}, Text: "ok", ReplyToID: 4509}}, RawCount: 1, OldestRawID: 3},
		},
		{
			name: "сообщение в топике без ответа",
			result: &tg.MessagesChannelMessages{Users: users, Chats: chats, Messages: []tg.MessageClass{
				&tg.Message{
					ID: 4, Date: date, PeerID: channelPeer, FromID: &tg.PeerUser{UserID: 777}, Message: "в топике",
					ReplyTo: topicRoot(12),
				},
			}},
			want: Page{Messages: []Message{{ID: 4, Date: wantDate, From: &Author{ID: 777, Name: "Ivan Petrov"}, Text: "в топике", TopicID: 12}}, RawCount: 1, OldestRawID: 4},
		},
		{
			name: "ответ внутри топика",
			result: &tg.MessagesChannelMessages{Users: users, Chats: chats, Messages: []tg.MessageClass{
				&tg.Message{
					ID: 5, Date: date, PeerID: channelPeer, FromID: &tg.PeerUser{UserID: 777}, Message: "ответ",
					ReplyTo: topicReply(4509, 12),
				},
			}},
			want: Page{Messages: []Message{{ID: 5, Date: wantDate, From: &Author{ID: 777, Name: "Ivan Petrov"}, Text: "ответ", ReplyToID: 4509, TopicID: 12}}, RawCount: 1, OldestRawID: 5},
		},
		{
			name: "пересылка из канала",
			result: &tg.MessagesChannelMessages{Users: users, Chats: chats, Messages: []tg.MessageClass{
				&tg.Message{
					ID: 6, Date: date, PeerID: channelPeer, FromID: &tg.PeerUser{UserID: 777}, Message: "fwd",
					FwdFrom: fwdHeader(&tg.PeerChannel{ChannelID: 400}, ""),
				},
			}},
			want: Page{Messages: []Message{{ID: 6, Date: wantDate, From: &Author{ID: 777, Name: "Ivan Petrov"}, Text: "fwd", ForwardedFrom: "Team alerts"}}, RawCount: 1, OldestRawID: 6},
		},
		{
			name: "пересылка от скрытого пользователя",
			result: &tg.MessagesChannelMessages{Users: users, Chats: chats, Messages: []tg.MessageClass{
				&tg.Message{
					ID: 7, Date: date, PeerID: channelPeer, FromID: &tg.PeerUser{UserID: 777}, Message: "fwd",
					FwdFrom: fwdHeader(nil, "Hidden Sender"),
				},
			}},
			want: Page{Messages: []Message{{ID: 7, Date: wantDate, From: &Author{ID: 777, Name: "Ivan Petrov"}, Text: "fwd", ForwardedFrom: "Hidden Sender"}}, RawCount: 1, OldestRawID: 7},
		},
		{
			name: "фото",
			result: &tg.MessagesChannelMessages{Users: users, Chats: chats, Messages: []tg.MessageClass{
				&tg.Message{ID: 8, Date: date, PeerID: channelPeer, FromID: &tg.PeerUser{UserID: 777}, Media: &tg.MessageMediaPhoto{}},
			}},
			want: Page{Messages: []Message{{ID: 8, Date: wantDate, From: &Author{ID: 777, Name: "Ivan Petrov"}, Media: "photo"}}, RawCount: 1, OldestRawID: 8},
		},
		{
			name: "документ с именем файла",
			result: &tg.MessagesChannelMessages{Users: users, Chats: chats, Messages: []tg.MessageClass{
				&tg.Message{
					ID: 9, Date: date, PeerID: channelPeer, FromID: &tg.PeerUser{UserID: 777},
					Media: &tg.MessageMediaDocument{Document: &tg.Document{
						MimeType:   "application/pdf",
						Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "report.pdf"}},
					}},
				},
			}},
			want: Page{Messages: []Message{{ID: 9, Date: wantDate, From: &Author{ID: 777, Name: "Ivan Petrov"}, Media: "document: report.pdf"}}, RawCount: 1, OldestRawID: 9},
		},
		{
			name: "видео с именем файла",
			result: &tg.MessagesChannelMessages{Users: users, Chats: chats, Messages: []tg.MessageClass{
				&tg.Message{
					ID: 10, Date: date, PeerID: channelPeer, FromID: &tg.PeerUser{UserID: 777},
					Media: &tg.MessageMediaDocument{Video: true, Document: &tg.Document{
						MimeType:   "video/mp4",
						Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "demo.mp4"}},
					}},
				},
			}},
			want: Page{Messages: []Message{{ID: 10, Date: wantDate, From: &Author{ID: 777, Name: "Ivan Petrov"}, Media: "video: demo.mp4"}}, RawCount: 1, OldestRawID: 10},
		},
		{
			name: "документ без имени — mime",
			result: &tg.MessagesChannelMessages{Users: users, Chats: chats, Messages: []tg.MessageClass{
				&tg.Message{
					ID: 11, Date: date, PeerID: channelPeer, FromID: &tg.PeerUser{UserID: 777},
					Media: &tg.MessageMediaDocument{Document: &tg.Document{MimeType: "image/gif"}},
				},
			}},
			want: Page{Messages: []Message{{ID: 11, Date: wantDate, From: &Author{ID: 777, Name: "Ivan Petrov"}, Media: "document: image/gif"}}, RawCount: 1, OldestRawID: 11},
		},
		{
			name: "голосовое",
			result: &tg.MessagesChannelMessages{Users: users, Chats: chats, Messages: []tg.MessageClass{
				&tg.Message{
					ID: 12, Date: date, PeerID: channelPeer, FromID: &tg.PeerUser{UserID: 777},
					Media: &tg.MessageMediaDocument{Voice: true, Document: &tg.Document{MimeType: "audio/ogg"}},
				},
			}},
			want: Page{Messages: []Message{{ID: 12, Date: wantDate, From: &Author{ID: 777, Name: "Ivan Petrov"}, Media: "voice"}}, RawCount: 1, OldestRawID: 12},
		},
		{
			name: "стикер",
			result: &tg.MessagesChannelMessages{Users: users, Chats: chats, Messages: []tg.MessageClass{
				&tg.Message{
					ID: 13, Date: date, PeerID: channelPeer, FromID: &tg.PeerUser{UserID: 777},
					Media: &tg.MessageMediaDocument{Document: &tg.Document{
						MimeType:   "image/webp",
						Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "sticker.webp"}, &tg.DocumentAttributeSticker{}},
					}},
				},
			}},
			want: Page{Messages: []Message{{ID: 13, Date: wantDate, From: &Author{ID: 777, Name: "Ivan Petrov"}, Media: "sticker"}}, RawCount: 1, OldestRawID: 13},
		},
		{
			name: "неизвестное медиа — имя типа TL",
			result: &tg.MessagesChannelMessages{Users: users, Chats: chats, Messages: []tg.MessageClass{
				&tg.Message{ID: 14, Date: date, PeerID: channelPeer, FromID: &tg.PeerUser{UserID: 777}, Media: &tg.MessageMediaDice{}},
			}},
			want: Page{Messages: []Message{{ID: 14, Date: wantDate, From: &Author{ID: 777, Name: "Ivan Petrov"}, Media: "messageMediaDice"}}, RawCount: 1, OldestRawID: 14},
		},
		{
			name: "служебные и пустые сообщения не попадают в Messages, но учитываются в RawCount и OldestRawID",
			result: &tg.MessagesMessagesSlice{Users: users, Chats: chats, Messages: []tg.MessageClass{
				&tg.MessageService{ID: 15, Date: date, PeerID: channelPeer},
				&tg.MessageEmpty{ID: 16},
				&tg.Message{ID: 17, Date: date, PeerID: channelPeer, FromID: &tg.PeerUser{UserID: 777}, Message: "x"},
			}},
			want: Page{Messages: []Message{{ID: 17, Date: wantDate, From: &Author{ID: 777, Name: "Ivan Petrov"}, Text: "x"}}, RawCount: 3, OldestRawID: 15},
		},
		{
			name:   "notModified — пусто",
			result: &tg.MessagesMessagesNotModified{Count: 3},
			want:   Page{Messages: []Message{}},
		},
		{
			name: "исходящее в личке без from_id — автор не указан",
			result: &tg.MessagesMessages{Users: users, Messages: []tg.MessageClass{
				&tg.Message{ID: 18, Date: date, Out: true, PeerID: &tg.PeerUser{UserID: 777}, Message: "привет"},
			}},
			want: Page{Messages: []Message{{ID: 18, Date: wantDate, Outgoing: true, Text: "привет"}}, RawCount: 1, OldestRawID: 18},
		},
		{
			name: "исходящий пост канала без from_id",
			result: &tg.MessagesChannelMessages{Chats: chats, Messages: []tg.MessageClass{
				&tg.Message{ID: 21, Date: date, Out: true, Post: true, PeerID: channelPeer, Message: "пост"},
			}},
			want: Page{Messages: []Message{{ID: 21, Date: wantDate, From: &Author{ID: 300, Name: "Team oncall"}, Text: "пост", Outgoing: true}}, RawCount: 1, OldestRawID: 21},
		},
		{
			name: "входящее в личке без from_id — автор собеседник",
			result: &tg.MessagesMessages{Users: users, Messages: []tg.MessageClass{
				&tg.Message{ID: 19, Date: date, PeerID: &tg.PeerUser{UserID: 777}, Message: "привет"},
			}},
			want: Page{Messages: []Message{{ID: 19, Date: wantDate, From: &Author{ID: 777, Name: "Ivan Petrov"}, Text: "привет"}}, RawCount: 1, OldestRawID: 19},
		},
		{
			name: "пересылка из канала, которого нет в ответе — id текстом",
			result: &tg.MessagesChannelMessages{Users: users, Chats: chats, Messages: []tg.MessageClass{
				&tg.Message{
					ID: 20, Date: date, PeerID: channelPeer, FromID: &tg.PeerUser{UserID: 777}, Message: "fwd",
					FwdFrom: fwdHeader(&tg.PeerChannel{ChannelID: 123}, ""),
				},
			}},
			want: Page{Messages: []Message{{ID: 20, Date: wantDate, From: &Author{ID: 777, Name: "Ivan Petrov"}, Text: "fwd", ForwardedFrom: "channel 123"}}, RawCount: 1, OldestRawID: 20},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, pageFromResult(tt.result))
		})
	}
}

func TestTopicsFromResult(t *testing.T) {
	tests := []struct {
		name   string
		topics []tg.ForumTopicClass
		want   []Topic
	}{
		{
			name: "открытый, закрытый и удалённый",
			topics: []tg.ForumTopicClass{
				&tg.ForumTopic{ID: 1, Title: "General"},
				&tg.ForumTopic{ID: 12, Title: "Incidents", Closed: true},
				&tg.ForumTopicDeleted{ID: 13},
			},
			want: []Topic{{ID: 1, Title: "General"}, {ID: 12, Title: "Incidents", Closed: true}},
		},
		{name: "пусто", want: []Topic{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, topicsFromResult(tt.topics))
		})
	}
}

func replyTo(msgID int) *tg.MessageReplyHeader {
	header := &tg.MessageReplyHeader{}
	header.SetReplyToMsgID(msgID)
	return header
}

func topicRoot(topicID int) *tg.MessageReplyHeader {
	header := &tg.MessageReplyHeader{ForumTopic: true}
	header.SetReplyToMsgID(topicID)
	return header
}

func topicReply(msgID, topicID int) *tg.MessageReplyHeader {
	header := &tg.MessageReplyHeader{ForumTopic: true}
	header.SetReplyToMsgID(msgID)
	header.SetReplyToTopID(topicID)
	return header
}

func fwdHeader(from tg.PeerClass, fromName string) tg.MessageFwdHeader {
	var header tg.MessageFwdHeader
	if from != nil {
		header.SetFromID(from)
	}
	if fromName != "" {
		header.SetFromName(fromName)
	}
	return header
}
