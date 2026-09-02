package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2/widget"

	"github.com/rohankewalramani/inventory-sys/internal/core"
)

// parseQuantity reads a whole-number quantity, treating an empty field as
// zero so that "no opening stock" does not require typing a 0.
func parseQuantity(s string) (int64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return 0, nil
	}

	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q is not a whole number", core.ErrInvalid, s)
	}
	return n, nil
}

// formatQuantity renders a count with thousands separators, so six figures of
// cable ties are readable at a glance.
func formatQuantity(n int64) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	return sign + groupDigits(strconv.FormatInt(n, 10))
}

func groupDigits(s string) string {
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// describeCount renders a listing summary, saying explicitly when the view is
// truncated so nobody concludes the missing rows were deleted.
func describeCount(shown, total int) string {
	if shown == total {
		if total == 1 {
			return "1 product"
		}
		return fmt.Sprintf("%s products", formatQuantity(int64(total)))
	}
	return fmt.Sprintf("showing %s of %s", formatQuantity(int64(shown)), formatQuantity(int64(total)))
}

// truncate shortens a string, marking that it was cut.
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + "…"
}

// statusImportance maps a stock status to the colour the whole application
// uses for it, so amber always means the same thing.
func statusImportance(status core.StockStatus) widget.Importance {
	switch status {
	case core.StatusOutOfStock:
		return widget.DangerImportance
	case core.StatusLow:
		return widget.WarningImportance
	case core.StatusUntracked:
		return widget.LowImportance
	}
	return widget.SuccessImportance
}

// quantityImportance colours a stock figure. Negative stock is always an error
// worth seeing, whatever the product's reorder point says.
func quantityImportance(p core.ProductWithStock) widget.Importance {
	if p.NonStock {
		return widget.LowImportance
	}
	if p.OnHand < 0 {
		return widget.DangerImportance
	}
	return statusImportance(p.Status())
}

// formatDate renders a calendar date.
func formatDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("2 Jan 2006")
}

// formatDateTime renders a timestamp for the activity list.
func formatDateTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("2 Jan 2006, 15:04")
}

// formatRelative renders how long ago something happened, which is what a
// person actually wants from an activity feed.
func formatRelative(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "never"
	}

	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hr ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 30*24*time.Hour:
		return agoUnits(int(d.Hours()/24), "day")
	case d < 365*24*time.Hour:
		return agoUnits(int(d.Hours()/24/30), "month")
	}
	return agoUnits(int(d.Hours()/24/365), "year")
}

// agoUnits renders "1 month ago" rather than "1 months ago".
func agoUnits(n int, unit string) string {
	if n == 1 {
		return "1 " + unit + " ago"
	}
	return fmt.Sprintf("%d %ss ago", n, unit)
}

// formatDelta renders a signed quantity change with an explicit sign, so an
// increase and a decrease are distinguishable without reading the reason.
func formatDelta(n int64) string {
	if n > 0 {
		return "+" + formatQuantity(n)
	}
	return formatQuantity(n)
}

// deltaImportance colours a ledger movement by direction.
func deltaImportance(n int64) widget.Importance {
	if n > 0 {
		return widget.SuccessImportance
	}
	if n < 0 {
		return widget.WarningImportance
	}
	return widget.MediumImportance
}

// dash renders an empty string as an em dash, so a blank column reads as
// "nothing here" rather than as a rendering failure.
func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// pluralize returns the singular or plural form for a count.
func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%s %s", formatQuantity(int64(n)), plural)
}
