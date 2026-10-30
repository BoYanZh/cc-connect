package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/chenhg5/cc-connect/core"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

var _ core.StreamingCardPlatform = (*Platform)(nil)

const (
	// telegramCardUpdateMinInterval throttles in-place edits of the
	// streaming message so a fast turn does not hit Telegram rate limits.
	telegramCardUpdateMinInterval = 1500 * time.Millisecond
	// telegramCardMaxText bounds a single edit; Telegram rejects text
	// messages over 4096 characters.
	telegramCardMaxText = 4000
)

// telegramStreamingCard aggregates a whole agent turn into a single Telegram
// message that is edited in place, instead of one message per event.
// Implements core.StreamingCard — when present, the engine routes the entire
// turn through Update/Finalize and skips per-event sends, so a long turn
// shows as one live-updating message rather than chat spam.
type telegramStreamingCard struct {
	platform *Platform
	bot      telegramBot
	chatID   int64
	threadID int
	replyTo  int

	mu         sync.Mutex
	msgID      int // 0 until the first post
	failed     bool
	lastUpdate time.Time
	lastSent   string
}

// CreateStreamingCard prepares a lazy streaming card; the Telegram message is
// not posted until the first content arrives.
func (p *Platform) CreateStreamingCard(ctx context.Context, rctx any) (core.StreamingCard, error) {
	rc, ok := rctx.(replyContext)
	if !ok {
		return nil, fmt.Errorf("telegram: invalid reply context type %T", rctx)
	}
	bot, err := p.connectedBot("streaming card")
	if err != nil {
		return nil, err
	}
	return &telegramStreamingCard{
		platform: p,
		bot:      bot,
		chatID:   rc.chatID,
		threadID: rc.threadID,
		replyTo:  rc.messageID,
	}, nil
}

// sendParams builds SendMessage params for one chunk.
func (c *telegramStreamingCard) sendParams(chunk string, first, html bool) *tgbot.SendMessageParams {
	params := &tgbot.SendMessageParams{
		ChatID:          c.chatID,
		MessageThreadID: c.threadID,
		Text:            chunk,
	}
	if html {
		params.ParseMode = models.ParseModeHTML
	}
	if first && c.replyTo != 0 {
		params.ReplyParameters = &models.ReplyParameters{MessageID: c.replyTo}
	}
	return params
}

// postFresh posts content as one or more fresh messages, recording the first
// message ID for subsequent edits. Caller must hold c.mu.
func (c *telegramStreamingCard) postFresh(ctx context.Context, content string) error {
	html := core.MarkdownToSimpleHTML(content)
	chunks, useHTML := core.SplitMessageCodeFenceAware(html, telegramMaxMessageLen), true
	sent, err := c.bot.SendMessage(ctx, c.sendParams(chunks[0], true, true))
	if err != nil {
		if !strings.Contains(err.Error(), "can't parse") {
			return err
		}
		useHTML = false
		chunks = core.SplitMessageCodeFenceAware(content, telegramMaxMessageLen)
		sent, err = c.bot.SendMessage(ctx, c.sendParams(chunks[0], true, false))
		if err != nil {
			return err
		}
	}
	c.msgID = sent.ID
	for _, chunk := range chunks[1:] {
		if _, err := c.bot.SendMessage(ctx, c.sendParams(chunk, false, useHTML)); err != nil {
			return err
		}
	}
	return nil
}

// render posts the card on first use, then edits it in place thereafter.
// Caller must hold c.mu.
func (c *telegramStreamingCard) render(ctx context.Context, content string) error {
	if c.msgID == 0 {
		return c.postFresh(ctx, content)
	}
	html := core.MarkdownToSimpleHTML(content)
	_, err := c.bot.EditMessageText(ctx, &tgbot.EditMessageTextParams{
		ChatID:    c.chatID,
		MessageID: c.msgID,
		Text:      html,
		ParseMode: models.ParseModeHTML,
	})
	if err != nil && strings.Contains(err.Error(), "can't parse") {
		_, err = c.bot.EditMessageText(ctx, &tgbot.EditMessageTextParams{
			ChatID:    c.chatID,
			MessageID: c.msgID,
			Text:      content,
		})
	}
	return err
}

// Update renders the latest aggregated content. The first post is immediate;
// subsequent edits are coalesced to telegramCardUpdateMinInterval. Transient
// errors are swallowed (Finalize retries) so a blip doesn't abort the turn.
//
// Once the rendered payload exceeds the edit size limit we stop attempting
// in-place edits (which would fail); the existing card stays at the last
// fitting snapshot and Finalize delivers the full reply.
func (c *telegramStreamingCard) Update(ctx context.Context, content string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failed || content == "" {
		return nil
	}
	if c.msgID != 0 && time.Since(c.lastUpdate) < telegramCardUpdateMinInterval {
		return nil
	}
	if rendered := core.MarkdownToSimpleHTML(content); rendered == "" || rendered == c.lastSent {
		return nil
	} else if c.msgID != 0 && len([]rune(rendered)) > telegramCardMaxText {
		slog.Debug("telegram: streaming card update skipped: payload exceeds edit limit",
			"size", len([]rune(rendered)), "limit", telegramCardMaxText)
		return nil
	}
	if err := c.render(ctx, content); err != nil {
		if isTelegramNotModified(err) {
			c.lastSent = core.MarkdownToSimpleHTML(content)
			return nil
		}
		slog.Debug("telegram: streaming card update failed (will retry on next tick / finalize)", "error", err)
		return nil
	}
	c.lastUpdate = time.Now()
	c.lastSent = core.MarkdownToSimpleHTML(content)
	return nil
}

// Finalize writes the final content unconditionally (no throttle); it posts
// the card if it was never posted. When the final payload exceeds the edit
// size limit AND a card has already been posted, the full reply is delivered
// as fresh chunked message(s) — the partial card stays visible above them.
// The engine sees success, so no fallback duplicate is sent.
func (c *telegramStreamingCard) Finalize(ctx context.Context, content string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failed {
		return nil
	}
	rendered := core.MarkdownToSimpleHTML(content)
	if rendered == "" || rendered == c.lastSent {
		return nil
	}
	if c.msgID != 0 && len([]rune(rendered)) > telegramCardMaxText {
		slog.Debug("telegram: streaming card finalize switching to fresh post (payload exceeds edit limit)",
			"size", len([]rune(rendered)), "limit", telegramCardMaxText)
		if err := c.postFresh(ctx, content); err != nil {
			c.failed = true
			return fmt.Errorf("telegram: finalize streaming card: %w", err)
		}
		c.lastSent = rendered
		return nil
	}
	if err := c.render(ctx, content); err != nil {
		c.failed = true
		return fmt.Errorf("telegram: finalize streaming card: %w", err)
	}
	c.lastSent = rendered
	return nil
}

// Failed returns true if the card has entered a failed state.
func (c *telegramStreamingCard) Failed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failed
}

// isTelegramNotModified reports the "message is not modified" API error,
// which happens when an update carries content identical to the current
// message. It is not a failure.
func isTelegramNotModified(err error) bool {
	return err != nil && strings.Contains(err.Error(), "message is not modified")
}
