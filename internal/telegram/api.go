package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}
type Chat struct {
	ID int64 `json:"id"`
}
type Message struct {
	MessageID int    `json:"message_id"`
	From      User   `json:"from"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}
type CallbackQuery struct {
	ID      string  `json:"id"`
	From    User    `json:"from"`
	Message Message `json:"message"`
	Data    string  `json:"data"`
}
type Update struct {
	UpdateID int            `json:"update_id"`
	Message  *Message       `json:"message"`
	Callback *CallbackQuery `json:"callback_query"`
}
type Button struct {
	Text         string `json:"text"`
	URL          string `json:"url,omitempty"`
	CallbackData string `json:"callback_data,omitempty"`
}
type Markup struct {
	InlineKeyboard [][]Button `json:"inline_keyboard"`
}
type Client struct {
	base string
	http *http.Client
}

func NewClient(token string) *Client {
	return &Client{"https://api.telegram.org/bot" + token, &http.Client{Timeout: 35 * time.Second}}
}
func (c *Client) call(ctx context.Context, method string, payload any, out any) error {
	b, _ := json.Marshal(payload)
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+method, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.http.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	var env struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if e = json.NewDecoder(resp.Body).Decode(&env); e != nil {
		return e
	}
	if !env.OK {
		return fmt.Errorf("telegram %s: %s", method, env.Description)
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}
func (c *Client) Updates(ctx context.Context, offset int) ([]Update, error) {
	var out []Update
	e := c.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 25, "allowed_updates": []string{"message", "callback_query"}}, &out)
	return out, e
}
func (c *Client) Send(ctx context.Context, chat int64, text string, k Markup) (Message, error) {
	var out Message
	e := c.call(ctx, "sendMessage", map[string]any{"chat_id": chat, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true, "reply_markup": k}, &out)
	return out, e
}
func (c *Client) Edit(ctx context.Context, chat int64, msg int, text string, k Markup) error {
	return c.call(ctx, "editMessageText", map[string]any{"chat_id": chat, "message_id": msg, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true, "reply_markup": k}, nil)
}
func (c *Client) Answer(ctx context.Context, id, text string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}
func (c *Client) Delete(ctx context.Context, chat int64, message int) error {
	return c.call(ctx, "deleteMessage", map[string]any{"chat_id": chat, "message_id": message}, nil)
}
