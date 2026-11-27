//go:build linux && vivagtk

package linux

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/vivaui/viva/text"
)

func TestGTKNativeDirectFcitxRecoversAfterDaemonRestart(t *testing.T) {
	if os.Getenv("VIVA_TEST_FCITX_RESTART") != "1" || !strings.HasPrefix(os.Getenv("DBUS_SESSION_BUS_ADDRESS"), "unix:path=/tmp/") {
		t.Skip("requires a private GTK and D-Bus fixture with no running input method")
	}
	if exec.Command("fcitx5-remote", "--check", "-n").Run() == nil {
		t.Fatal("the fixture already has a Fcitx daemon")
	}
	t.Setenv("VIVA_GTK_FCITX_DIRECT", "")
	var daemon *exec.Cmd
	start := func() error {
		daemon = exec.Command("fcitx5", "-D", "--disable=notificationitem,notifications,classicui,xim,waylandim")
		return daemon.Start()
	}
	stop := func() {
		if daemon != nil && daemon.Process != nil {
			_ = daemon.Process.Kill()
			_ = daemon.Wait()
		}
		daemon = nil
	}
	t.Cleanup(stop)
	if err := start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for exec.Command("fcitx5-remote", "--check", "-n").Run() != nil {
		if time.Now().After(deadline) {
			t.Fatal("Fcitx did not appear on the fixture bus")
		}
		time.Sleep(15 * time.Millisecond)
	}
	testGTKNativeTentativeProbe(t, 1, func(p *gtkTentativeProbe) {
		p.asynchronous, p.keyIngress, p.tentative = true, true, true
		editor := text.NewEditor("abc")
		editor.MoveTo(3, false)
		editor.EnableTentativeComposition()
		p.install(editor)
		finish := func(failures ...string) {
			trial := gtkTentativeProbeTrial{name: "Fcitx service restart in the same window", value: editor.String(), failures: failures}
			for _, event := range p.events {
				trial.events = append(trial.events, event.Text)
			}
			p.trials = append(p.trials, trial)
			p.w.destroy()
		}
		probe := &gtkFcitxProbe{p: p}
		await := func(ready func() bool, then func()) {
			deadline := time.Now().Add(3 * time.Second)
			var poll func()
			poll = func() {
				if ready() {
					then()
				} else if time.Now().After(deadline) {
					finish("timed out waiting for input method state")
				} else {
					probe.later(10*time.Millisecond, poll)
				}
			}
			poll()
		}
		ready := func() bool {
			if d := p.w.ime.direct; d != nil {
				_, pending := d.client.Deadline()
				return !pending
			}
			return false
		}
		activate := func() bool {
			for _, args := range [][]string{{"-s", "pinyin"}, {"-o"}} {
				if err := exec.Command("fcitx5-remote", args...).Run(); err != nil {
					finish(err.Error())
					return false
				}
			}
			return true
		}
		press := func(key uint32) bool {
			if err := p.press(p.w.ime.ctx, key, 0); err != nil {
				finish(err.Error())
				return false
			}
			return true
		}
		await(ready, func() {
			if !activate() || !press('n') || !press('i') {
				return
			}
			await(func() bool { return editor.String() == "abcni" && editor.CommittedString() == "abc" }, func() {
				old := p.w.ime.direct
				stop()
				await(func() bool { return p.w.ime.direct == nil && editor.String() == "abc" }, func() {
					if !press('x') {
						return
					}
					await(func() bool { return editor.CommittedString() == "abcx" }, func() {
						if err := start(); err != nil {
							finish(err.Error())
							return
						}
						await(func() bool { return exec.Command("fcitx5-remote", "--check", "-n").Run() == nil }, func() {
							// Let GTK deliver the new owner notification before returning focus.
							probe.later(100*time.Millisecond, func() {
								p.w.ime.setFocused(false)
								p.w.ime.setFocused(true)
								if p.w.ime.direct == nil || p.w.ime.direct == old {
									finish("returning focus did not connect to the new Fcitx daemon")
									return
								}
								await(ready, func() {
									if !activate() || !press('n') || !press('i') || !press(' ') {
										return
									}
									await(func() bool { return editor.CommittedString() == "abcx\u4f60" }, func() { finish() })
								})
							})
						})
					})
				})
			})
		})
	})
}
