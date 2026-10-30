package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
)

func newStreamingTestPlatform(bot *stubTelegramBot) *Platform {
	p := &Platform{token: "token"}
	p.publishBot(bot, &models.User{ID: 42, Username: "mybot"})
	return p
}

// TestStreamingCard_FirstUpdatePosts verifies lazy posting: no message until
// the first content arrives, then exactly one SendMessage.
func TestStreamingCard_FirstUpdatePosts(t *testing.T) {
	bot := newStubTelegramBot()
	p := newStreamingTestPlatform(bot)
	sc, err := p.CreateStreamingCard(context.Background(), replyContext{chatID: 1, threadID: 0, messageID: 7})
	if err != nil {
		t.Fatalf("CreateStreamingCard: %v", err)
	}
	card := sc.(*telegramStreamingCard)
	if err := card.Update(context.Background(), "hello"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := bot.sendMessageCalls; got != 1 {
		t.Fatalf("sendMessageCalls = %d, want 1", got)
	}
	if got := bot.editMessageTextCalls; got != 0 {
		t.Fatalf("editMessageTextCalls = %d, want 0", got)
	}
	if card.msgID == 0 {
		t.Fatal("card message ID not recorded after first post")
	}
}

// TestStreamingCard_ThrottlesRapidEdits verifies that back-to-back updates
// within the throttle window do not each hit the API.
func TestStreamingCard_ThrottlesRapidEdits(t *testing.T) {
	bot := newStubTelegramBot()
	p := newStreamingTestPlatform(bot)
	sc, err := p.CreateStreamingCard(context.Background(), replyContext{chatID: 1})
	if err != nil {
		t.Fatalf("CreateStreamingCard: %v", err)
	}
	card := sc.(*telegramStreamingCard)
	if err := card.Update(context.Background(), "v1"); err != nil {
		t.Fatalf("Update v1: %v", err)
	}
	if err := card.Update(context.Background(), "v2"); err != nil {
		t.Fatalf("Update v2: %v", err)
	}
	if got := bot.editMessageTextCalls; got != 0 {
		t.Fatalf("editMessageTextCalls = %d, want 0 (throttled)", got)
	}
	card.lastUpdate = time.Now().Add(-2 * telegramCardUpdateMinInterval)
	if err := card.Update(context.Background(), "v3"); err != nil {
		t.Fatalf("Update v3: %v", err)
	}
	if got := bot.editMessageTextCalls; got != 1 {
		t.Fatalf("editMessageTextCalls = %d, want 1", got)
	}
}

// TestStreamingCard_IdenticalContentSkipsEdit verifies no API call happens
// when the content did not change.
func TestStreamingCard_IdenticalContentSkipsEdit(t *testing.T) {
	bot := newStubTelegramBot()
	p := newStreamingTestPlatform(bot)
	sc, err := p.CreateStreamingCard(context.Background(), replyContext{chatID: 1})
	if err != nil {
		t.Fatalf("CreateStreamingCard: %v", err)
	}
	card := sc.(*telegramStreamingCard)
	if err := card.Update(context.Background(), "same"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	card.lastUpdate = time.Now().Add(-2 * telegramCardUpdateMinInterval)
	if err := card.Update(context.Background(), "same"); err != nil {
		t.Fatalf("Update identical: %v", err)
	}
	if got := bot.editMessageTextCalls; got != 0 {
		t.Fatalf("editMessageTextCalls = %d, want 0", got)
	}
}

// TestStreamingCard_NotModifiedIsSuccess verifies the Telegram
// "message is not modified" error is swallowed.
func TestStreamingCard_NotModifiedIsSuccess(t *testing.T) {
	bot := newStubTelegramBot()
	p := newStreamingTestPlatform(bot)
	sc, err := p.CreateStreamingCard(context.Background(), replyContext{chatID: 1})
	if err != nil {
		t.Fatalf("CreateStreamingCard: %v", err)
	}
	card := sc.(*telegramStreamingCard)
	if err := card.Update(context.Background(), "v1"); err != nil {
		t.Fatalf("Update v1: %v", err)
	}
	bot.editErr = errors.New("Bad Request: message is not modified")
	card.lastUpdate = time.Now().Add(-2 * telegramCardUpdateMinInterval)
	if err := card.Update(context.Background(), "v2"); err != nil {
		t.Fatalf("Update with not-modified error: %v", err)
	}
	if card.Failed() {
		t.Fatal("card marked failed on not-modified")
	}
}

// TestStreamingCard_OverflowFinalizePostsFresh verifies that over-limit final
// content is delivered as fresh message(s) instead of a failing edit.
func TestStreamingCard_OverflowFinalizePostsFresh(t *testing.T) {
	bot := newStubTelegramBot()
	p := newStreamingTestPlatform(bot)
	sc, err := p.CreateStreamingCard(context.Background(), replyContext{chatID: 1})
	if err != nil {
		t.Fatalf("CreateStreamingCard: %v", err)
	}
	card := sc.(*telegramStreamingCard)
	if err := card.Update(context.Background(), "short"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	big := strings.Repeat("x", telegramCardMaxText+100)
	if err := card.Finalize(context.Background(), big); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if got := bot.sendMessageCalls; got < 2 {
		t.Fatalf("sendMessageCalls = %d, want >= 2 (card + fresh final)", got)
	}
	if card.Failed() {
		t.Fatal("card marked failed after successful overflow finalize")
	}
}

// TestStreamingCard_EmptyContentNoop verifies empty updates never post.
func TestStreamingCard_EmptyContentNoop(t *testing.T) {
	bot := newStubTelegramBot()
	p := newStreamingTestPlatform(bot)
	sc, err := p.CreateStreamingCard(context.Background(), replyContext{chatID: 1})
	if err != nil {
		t.Fatalf("CreateStreamingCard: %v", err)
	}
	card := sc.(*telegramStreamingCard)
	if err := card.Update(context.Background(), ""); err != nil {
		t.Fatalf("Update empty: %v", err)
	}
	if err := card.Finalize(context.Background(), ""); err != nil {
		t.Fatalf("Finalize empty: %v", err)
	}
	if got := bot.sendMessageCalls; got != 0 {
		t.Fatalf("sendMessageCalls = %d, want 0", got)
	}
}

// TestCreateStreamingCard_BadReplyCtx verifies invalid contexts are rejected.
func TestCreateStreamingCard_BadReplyCtx(t *testing.T) {
	bot := newStubTelegramBot()
	p := newStreamingTestPlatform(bot)
	if _, err := p.CreateStreamingCard(context.Background(), "nope"); err == nil {
		t.Fatal("expected error for invalid reply context, got nil")
	}
}
