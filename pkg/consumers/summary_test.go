package consumers

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/appximo/appximo/pkg/telegram"
	"github.com/appximo/appximo/pkg/worker"
)

// VOZ-VISUAL-S1: the scheduled digest goes out as PICTURE + TEXT. These tests
// drive the consumer against a fake engine (text + png endpoints) and a fake
// Telegram API and pin: the photo carries the text as caption; a retryable
// Telegram failure keeps the row pending (error returned); a refused photo
// (400) delivers the text; an engine without the image endpoint delivers text.

type fakeEngine struct {
	pngStatus int // 0 = serve a real png; else answer this status
}

func (e *fakeEngine) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/summary" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("format") == "png" {
			if e.pngStatus != 0 {
				w.WriteHeader(e.pngStatus)
				return
			}
			var buf bytes.Buffer
			png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))) //nolint:errcheck
			w.Header().Set("Content-Type", "image/png")
			w.Write(buf.Bytes()) //nolint:errcheck
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"text": "📋 <b>Resumen</b>\n🔴 3 esperan acción", "level": "red"}) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv
}

type fakeTGAPI struct {
	mu          sync.Mutex
	photos      []string // captions
	messages    []string
	photoStatus int // 0 = ok; else this status with ok:false
}

func (f *fakeTGAPI) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendPhoto"):
			if f.photoStatus != 0 {
				w.WriteHeader(f.photoStatus)
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "nope"}) //nolint:errcheck
				return
			}
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				t.Errorf("multipart: %v", err)
			}
			file, _, err := r.FormFile("photo")
			if err != nil {
				t.Errorf("photo part: %v", err)
			} else {
				b, _ := io.ReadAll(file)
				if len(b) == 0 {
					t.Error("empty photo")
				}
			}
			f.photos = append(f.photos, r.FormValue("caption"))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var body struct {
				Text string `json:"text"`
			}
			json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
			f.messages = append(f.messages, body.Text)
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 1}}) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newSummaryUnderTest(t *testing.T, eng *fakeEngine, tg *fakeTGAPI) *SummaryProcessor {
	engSrv := eng.server(t)
	tgSrv := tg.server(t)
	client := worker.NewEngineClient(engSrv.URL, "localhost", "a-test-secret-of-at-least-32-characters!!", "dueno", 0)
	tgc, err := telegram.New("123456:AAExampleExampleExampleExample01", "8851136988")
	if err != nil {
		t.Fatal(err)
	}
	tgc.SetHTTPClient(tgSrv.Client())
	tgc.SetAPIBase(tgSrv.URL)
	return NewSummaryProcessor(client, tgc, "summary.telegram", zerolog.Nop())
}

func TestSummaryProcessor_PhotoWithCaption(t *testing.T) {
	tg := &fakeTGAPI{}
	p := newSummaryUnderTest(t, &fakeEngine{}, tg)
	if err := p.Process(context.Background(), worker.Row{TenantID: "tiendita", Topic: "summary.telegram"}); err != nil {
		t.Fatal(err)
	}
	if len(tg.photos) != 1 || !strings.Contains(tg.photos[0], "esperan acción") {
		t.Fatalf("want one photo captioned with the digest, got %v", tg.photos)
	}
	if len(tg.messages) != 0 {
		t.Fatalf("no separate text when the caption fits, got %v", tg.messages)
	}
}

func TestSummaryProcessor_TelegramDownKeepsRowPending(t *testing.T) {
	tg := &fakeTGAPI{photoStatus: http.StatusBadGateway}
	p := newSummaryUnderTest(t, &fakeEngine{}, tg)
	if err := p.Process(context.Background(), worker.Row{TenantID: "tiendita"}); err == nil {
		t.Fatal("a 5xx from Telegram must be returned so the outbox retries")
	}
	if len(tg.messages) != 0 {
		t.Fatalf("nothing must be marked delivered on a retryable failure, got %v", tg.messages)
	}
}

func TestSummaryProcessor_PhotoRefusedDeliversText(t *testing.T) {
	tg := &fakeTGAPI{photoStatus: http.StatusBadRequest}
	p := newSummaryUnderTest(t, &fakeEngine{}, tg)
	if err := p.Process(context.Background(), worker.Row{TenantID: "tiendita"}); err != nil {
		t.Fatalf("a refused photo must not fail the row when the text was delivered: %v", err)
	}
	if len(tg.messages) != 1 || !strings.Contains(tg.messages[0], "esperan acción") {
		t.Fatalf("text must be delivered instead, got %v", tg.messages)
	}
}

func TestSummaryProcessor_EngineWithoutImageSendsText(t *testing.T) {
	tg := &fakeTGAPI{}
	p := newSummaryUnderTest(t, &fakeEngine{pngStatus: http.StatusNotFound}, tg)
	if err := p.Process(context.Background(), worker.Row{TenantID: "tiendita"}); err != nil {
		t.Fatal(err)
	}
	if len(tg.photos) != 0 || len(tg.messages) != 1 {
		t.Fatalf("older engine → text only; photos=%v messages=%v", tg.photos, tg.messages)
	}
}
