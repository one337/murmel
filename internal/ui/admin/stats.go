package admin

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
)

// statsRefresh is how often an open statistics dialog asks for new
// numbers, like the official client's user information window.
const statsRefresh = 5 * time.Second

// ShowUserStats opens the statistics of a user (version, connection,
// ping and voice packet counters) and refreshes them while open.
func ShowUserStats(parent gtk.Widgetter, b *bus.Bus, session uint32, name string) {
	page := adw.NewPreferencesPage()

	spinner := adw.NewSpinner()
	spinner.SetSizeRequest(32, 32)
	spinner.SetMarginTop(48)
	spinner.SetHAlign(gtk.AlignCenter)
	stack := gtk.NewStack()
	stack.AddNamed(spinner, "loading")
	stack.AddNamed(page, "stats")
	msg := gtk.NewLabel("")
	msg.AddCSSClass("dim-label")
	msg.SetWrap(true)
	msg.SetMarginTop(48)
	msg.SetMarginStart(24)
	msg.SetMarginEnd(24)
	stack.AddNamed(msg, "message")

	hdr := adw.NewHeaderBar()
	hdr.SetTitleWidget(adw.NewWindowTitle(tr("Statistics"), name))
	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(hdr)
	toolbar.SetContent(stack)

	dialog := adw.NewDialog()
	dialog.SetTitle(tr("Statistics"))
	dialog.SetContentWidth(460)
	dialog.SetContentHeight(560)
	dialog.SetChild(toolbar)

	rows := newStatRows(page)
	got := false
	subscribeWhileOpen(dialog, b, func(ev bus.UserStatsFetched) {
		if ev.Session != session {
			return
		}
		got = true
		rows.show(ev)
		stack.SetVisibleChildName("stats")
	})
	subscribeWhileOpen(dialog, b, func(ev bus.PermissionDenied) {
		if got {
			return
		}
		text := tr("You do not have permission to view this on this server.")
		if ev.Text != "" {
			text = ev.Text
		}
		msg.SetText(text)
		stack.SetVisibleChildName("message")
	})

	ctx, cancel := context.WithCancel(context.Background())
	dialog.ConnectClosed(cancel)
	request := func() { b.Emit(bus.UserStatsRequested{Session: session}) }
	glib.TimeoutAdd(uint(statsRefresh/time.Millisecond), func() bool {
		if ctx.Err() != nil {
			return false
		}
		request()
		return true
	})
	request()
	dialog.Present(parent)
}

// statRows holds the property rows of the statistics page: the title is
// the caption, the subtitle the value (libadwaita "property" style).
type statRows struct {
	page   *adw.PreferencesPage
	rows   map[string]*adw.ActionRow
	groups map[*adw.PreferencesGroup][]string
}

func newStatRows(page *adw.PreferencesPage) *statRows {
	r := &statRows{page: page, rows: map[string]*adw.ActionRow{},
		groups: map[*adw.PreferencesGroup][]string{}}
	group := func(title string, keys ...[2]string) {
		g := adw.NewPreferencesGroup()
		g.SetTitle(title)
		for _, k := range keys {
			r.groups[g] = append(r.groups[g], k[0])
			row := adw.NewActionRow()
			row.AddCSSClass("property")
			row.SetTitle(k[1])
			row.SetSubtitleSelectable(true)
			g.Add(row)
			r.rows[k[0]] = row
		}
		page.Add(g)
	}
	group(tr("Client"),
		[2]string{"version", tr("Version")},
		[2]string{"os", tr("Operating system")},
		[2]string{"opus", tr("Opus codec")},
		[2]string{"cert", tr("Certificate")})
	group(tr("Connection"),
		[2]string{"online", tr("Online for")},
		[2]string{"idle", tr("Idle for")},
		[2]string{"bandwidth", tr("Bandwidth")},
		[2]string{"ip", tr("Address")},
		[2]string{"tcpping", tr("TCP ping")},
		[2]string{"udpping", tr("UDP ping")})
	group(tr("Voice Packets (UDP)"),
		[2]string{"fromclient", tr("Sent")},
		[2]string{"fromserver", tr("Received")})
	return r
}

// set shows value for key and hides the row when the server sent nothing.
func (r *statRows) set(key, value string) {
	r.rows[key].SetSubtitle(value)
	r.rows[key].SetVisible(value != "")
}

func (r *statRows) show(ev bus.UserStatsFetched) {
	version := ev.Release
	if ev.Version != "" {
		if version != "" {
			version += " "
		}
		version += "(" + ev.Version + ")"
	}
	r.set("version", version)
	os := ev.OS
	if ev.OSVersion != "" {
		os += " " + ev.OSVersion
	}
	r.set("os", os)
	if ev.Version != "" {
		r.set("opus", yesNo(ev.Opus))
	} else {
		r.set("opus", "")
	}
	cert := ""
	if ev.CertificateSubject != "" {
		cert = ev.CertificateSubject
		if ev.StrongCertificate {
			cert += " " + tr("(trusted)")
		} else {
			cert += " " + tr("(self-signed)")
		}
	}
	r.set("cert", cert)

	if !ev.Connected.IsZero() {
		r.set("online", duration(time.Since(ev.Connected)))
	} else {
		r.set("online", "")
	}
	r.set("idle", duration(ev.Idle))
	if ev.Bandwidth > 0 {
		r.set("bandwidth", fmt.Sprintf(tr("%.1f kb/s"), float64(ev.Bandwidth)/1000))
	} else {
		r.set("bandwidth", "")
	}
	r.set("ip", ev.IP)
	r.set("tcpping", ping(ev.TCPPingAvg, ev.TCPPingVar, ev.TCPPackets))
	r.set("udpping", ping(ev.UDPPingAvg, ev.UDPPingVar, ev.UDPPackets))
	r.set("fromclient", packets(ev.FromClient))
	r.set("fromserver", packets(ev.FromServer))

	// Groups the server sent nothing for disappear instead of showing an
	// empty box.
	for g, keys := range r.groups {
		visible := false
		for _, k := range keys {
			visible = visible || r.rows[k].Visible()
		}
		g.SetVisible(visible)
	}
	// Servers share client details (version, OS, certificate, address)
	// of other users only with administrators.
	if ev.Release == "" && ev.OS == "" && ev.Version == "" {
		r.page.SetDescription(tr("The server shares client details of other users only with administrators."))
	} else {
		r.page.SetDescription("")
	}
}

func yesNo(b bool) string {
	if b {
		return tr("Yes")
	}
	return tr("No")
}

func duration(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	switch {
	case h > 0:
		return fmt.Sprintf(tr("%d h %d min"), h, m)
	case m > 0:
		return fmt.Sprintf(tr("%d min %d s"), m, s)
	}
	return fmt.Sprintf(tr("%d s"), s)
}

func ping(avg, variance float32, count uint32) string {
	if count == 0 && avg == 0 {
		return ""
	}
	// The server reports the variance; ± shows the standard deviation.
	return fmt.Sprintf(tr("%.0f ms ± %.0f ms"), avg, math.Sqrt(float64(variance)))
}

func packets(p bus.PacketStats) string {
	if p == (bus.PacketStats{}) {
		return ""
	}
	return fmt.Sprintf(tr("%d good · %d late · %d lost"), p.Good, p.Late, p.Lost)
}
