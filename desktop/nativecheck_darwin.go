//go:build nativecheck

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/transport"
	"github.com/0xmhha/trace-agent/internal/usage"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// This alternative entry point is compiled only for the native acceptance target.
// It uses a disposable profile instead of the operator's Keychain and database.
type acceptanceVault string

// The app opens on the usage dashboard, which has none of what the probe
// looks for. Unless a trace view is already on screen, a stage first opens the
// trace address that shows it: the tasks in progress report that no source is
// attached, the overview carries the metrics of the attached one.
const (
	probeEmptyRoute   = "#live"
	probeHistoryRoute = "#overview"
)

// probeText is what the probe reads on screen, in one interface language.
type probeText struct {
	Connected string `json:"connected"`
	NoSources string `json:"noSources"`
	OneSource string `json:"oneSource"`
	Attempts  string `json:"attempts"`
	Overview  string `json:"overview"`
}

// probeTexts holds the probe's texts per locale code of the page. A fresh
// profile opens in English, the default; a profile that once chose another
// language keeps it, so the probe reads the language from <html lang> and
// falls back to English.
func probeTexts() map[usage.Locale]probeText {
	return map[usage.Locale]probeText{
		usage.LocaleEnglish: {Connected: "Connected", NoSources: "No sources are connected.", OneSource: "Current sources: 1", Attempts: "Tool attempts", Overview: "Overview"},
		usage.LocaleKorean:  {Connected: "연결됨", NoSources: "연결한 출처가 없습니다.", OneSource: "현재 출처 1개", Attempts: "도구 시도", Overview: "개요"},
	}
}

func (v acceptanceVault) Load() (registry, error) {
	data, err := os.ReadFile(string(v))
	if errors.Is(err, os.ErrNotExist) {
		return registry{}, nil
	}
	var value registry
	if err == nil {
		err = json.Unmarshal(data, &value)
	}
	return value, err
}
func (v acceptanceVault) Save(r registry) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(string(v), data, 0600)
}
func run() error {
	dir := os.Getenv("TRACE_NATIVE_PROFILE")
	if dir == "" || !filepath.IsAbs(dir) || !strings.HasPrefix(filepath.Base(dir), "trace-native-") {
		return errors.New("invalid acceptance profile")
	}
	base, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil || filepath.Dir(dir) != base {
		return errors.New("profile must be a new directory under the system temporary folder")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	restart := os.Getenv("TRACE_NATIVE_RESTART") == "1"
	texts, err := json.Marshal(probeTexts())
	if err != nil {
		return fmt.Errorf("encode probe texts: %w", err)
	}
	report := make(chan error, 1)
	probe := func(ctx context.Context, b *Bridge) {
		check := func(stage string) {
			route := probeHistoryRoute
			if stage == "empty" {
				route = probeEmptyRoute
			}
			command := `(()=>{const stage=` + fmt.Sprintf("%q", stage) + `,route=` + fmt.Sprintf("%q", route) + `,texts=` + string(texts) + `;
				if(!document.querySelector('.connection'))location.hash=route;
				let ticks=0;const timer=setInterval(()=>{
				const l=texts[document.documentElement.lang]||texts[` + fmt.Sprintf("%q", usage.LocaleDefault) + `];
				const text=document.body.innerText, heading=document.querySelector('.eyebrow')?.textContent || '';
				const connected=document.querySelector('.connection')?.textContent.includes(l.connected);
				if(connected && heading.includes('#') && (stage==='empty'?text.includes(l.noSources):text.includes(l.oneSource) && text.includes(l.attempts) && Array.from(document.querySelectorAll('.metric')).some(m=>m.textContent.includes(l.attempts)&&m.querySelector('strong')?.textContent==='1'))){
					clearInterval(timer);window.runtime.EventsEmit('native-checked',stage);
				}else if(stage==='history'&&!text.includes(l.attempts)){
					Array.from(document.querySelectorAll('nav button')).find(b=>b.textContent.includes(l.overview))?.click();
				}
				if(++ticks===100){clearInterval(timer);window.runtime.EventsEmit('native-checked','failed '+stage+' '+text.slice(0,500));}
			},200);})()`
			wruntime.WindowExecJS(ctx, command)
		}
		wruntime.EventsOn(ctx, "native-checked", func(data ...interface{}) {
			stage := fmt.Sprint(data...)
			switch stage {
			case "empty":
				at := time.Now().UTC().Format(time.RFC3339Nano)
				raw := fmt.Sprintf("{\"timestamp\":%q,\"type\":\"session_meta\",\"payload\":{\"id\":\"local\"}}\n{\"timestamp\":%q,\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\",\"turn_id\":\"t1\"}}\n{\"timestamp\":%q,\"type\":\"response_item\",\"payload\":{\"type\":\"function_call\",\"call_id\":\"c1\",\"name\":\"Read\"}}\n", at, at, at)
				path := filepath.Join(dir, "synthetic.jsonl")
				err := root.WriteFile("synthetic.jsonl", []byte(raw), 0600)
				if err == nil {
					var picker string
					picker, err = b.collector.offer(selectedSource{Kind: collect.Rollout, Path: path})
					if err == nil {
						_, err = b.collector.consume(ctx, transport.PickerAction{Operation: "attachSource", Client: "CODEX", PickerToken: picker, RetentionDays: 7})
					}
				}
				if err != nil {
					report <- err
					wruntime.Quit(ctx)
					return
				}
				check("history")
			case "history":
				report <- nil
				wruntime.Quit(ctx)
			default:
				report <- errors.New(stage)
				wruntime.Quit(ctx)
			}
		})
		go func() {
			select {
			case <-ctx.Done():
			case <-time.After(32 * time.Second):
				select {
				case report <- errors.New("no native render response"):
				default:
				}
				wruntime.Quit(ctx)
			}
		}()
		if restart {
			check("history")
		} else {
			check("empty")
		}
	}
	if err := runDesktop(dir, acceptanceVault(filepath.Join(dir, "qa-registry.json")), "org.0xmhha.trace-agent.nativecheck", probe); err != nil {
		return err
	}
	select {
	case err := <-report:
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return err
		}
		fmt.Println("native WKWebView rendered expected source count and historical attempt")
		return nil
	default:
		return errors.New("native window closed early")
	}
}
