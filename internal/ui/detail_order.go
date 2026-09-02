package ui

import (
	"context"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/rohankewalramani/inventory-sys/internal/core"
)

// orderDetailPanel is the inspector beside the orders list.
//
// It is built around the question a client actually asks: what is on this PO,
// where is it going, when does it have to leave, and can we ship it. The
// ship-to address and routing notes are shown in full because getting either
// wrong is what causes a chargeback.
type orderDetailPanel struct {
	app *App

	root  *fyne.Container
	empty *widget.Label
	body  *fyne.Container

	current core.ID

	poLabel      *widget.Label
	whoLabel     *widget.Label
	statusLabel  *widget.Label
	scheduleBox  *fyne.Container
	actionRow    *fyne.Container
	progressBar  *widget.ProgressBar
	progressText *widget.Label
	shipToBox    *fyne.Container
	routingBox   *fyne.Container
	linesBox     *fyne.Container
	totalsBox    *fyne.Container
}

func newOrderDetailPanel(a *App) *orderDetailPanel {
	p := &orderDetailPanel{app: a}
	p.build()
	return p
}

func (p *orderDetailPanel) object() fyne.CanvasObject { return p.root }

func (p *orderDetailPanel) build() {
	p.empty = widget.NewLabel("Select an order to see its lines,\nship-to address and delivery dates.")
	p.empty.Alignment = fyne.TextAlignCenter
	p.empty.Importance = widget.LowImportance
	p.empty.Wrapping = fyne.TextWrapWord

	p.poLabel = widget.NewLabel("")
	p.poLabel.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
	p.poLabel.SizeName = theme.SizeNameSubHeadingText

	p.whoLabel = widget.NewLabel("")
	p.whoLabel.Wrapping = fyne.TextWrapWord

	p.statusLabel = widget.NewLabel("")
	p.statusLabel.TextStyle = fyne.TextStyle{Bold: true}

	p.progressBar = widget.NewProgressBar()
	p.progressBar.TextFormatter = func() string { return "" }
	p.progressText = widget.NewLabel("")
	p.progressText.Importance = widget.LowImportance

	p.scheduleBox = container.New(newFormGrid())
	p.actionRow = container.NewVBox()
	p.shipToBox = container.NewVBox()
	p.routingBox = container.NewVBox()
	p.linesBox = container.NewVBox()
	p.totalsBox = container.New(newFormGrid())

	p.body = container.NewVBox(
		p.poLabel,
		p.whoLabel,
		widget.NewSeparator(),
		p.statusLabel,
		p.progressBar,
		p.progressText,
		p.actionRow,
		sectionHeading("Schedule"),
		p.scheduleBox,
		p.shipToBox,
		p.routingBox,
		sectionHeading("Lines"),
		p.linesBox,
		p.totalsBox,
	)
	p.body.Hide()

	p.root = container.NewPadded(container.NewVScroll(container.NewStack(p.empty, p.body)))
}

func (p *orderDetailPanel) clear() {
	p.current = core.NilID
	p.body.Hide()
	p.empty.Show()
}

// show loads and renders one order.
func (p *orderDetailPanel) show(id core.ID) {
	p.current = id
	p.empty.Hide()
	p.body.Show()

	p.poLabel.SetText("Loading…")
	p.whoLabel.SetText("")

	p.app.background(func(ctx context.Context) {
		detail, err := p.app.svc.GetOrder(ctx, id)
		p.app.onMain(func() {
			// The user may have clicked another row while this loaded.
			if p.current != id {
				return
			}
			if err != nil {
				p.app.showError(wrapf(err, "the order could not be loaded"))
				p.clear()
				return
			}
			p.render(detail)
		})
	})
}

func (p *orderDetailPanel) render(detail core.OrderDetail) {
	now := time.Now()

	p.poLabel.SetText(detail.CustomerPONumber)
	p.whoLabel.SetText(detail.Customer.Name + "  ·  " + detail.Store.Label())

	p.statusLabel.SetText(p.statusCommentary(detail, now))
	p.statusLabel.Importance = p.statusImportance(detail, now)
	p.statusLabel.Refresh()

	// Fyne draws a progress bar's empty track in a tint of the primary colour,
	// which at zero reads as a full bar rather than an empty one. Nothing has
	// shipped until there is a shipment, so the bar only appears once it has
	// something true to say.
	if detail.Totals.Shipped > 0 {
		p.progressBar.SetValue(detail.Totals.Progress())
		p.progressBar.Show()
		p.progressText.SetText(sprintf("%s of %s units shipped, %s still to go",
			formatQuantity(detail.Totals.Shipped), formatQuantity(detail.Totals.Units),
			formatQuantity(detail.Totals.Outstanding)))
	} else {
		p.progressBar.Hide()
		p.progressText.SetText(sprintf("%s units on %s, none shipped yet",
			formatQuantity(detail.Totals.Units),
			pluralize(detail.Totals.Lines, "line", "lines")))
	}

	p.renderActions(detail)
	p.renderSchedule(detail, now)
	p.renderShipTo(detail)
	p.renderLines(detail)
	p.renderTotals(detail)
}

// statusCommentary says what the status means for today, not just what it is.
func (p *orderDetailPanel) statusCommentary(detail core.OrderDetail, now time.Time) string {
	if !detail.Status.Open() {
		return detail.Status.Label()
	}

	if !detail.CancelAfterDate.IsZero() {
		if days := core.DaysUntil(detail.CancelAfterDate, now); days < 0 {
			return sprintf("%s — %d days past the cancel date", detail.Status.Label(), -days)
		}
	}
	if !detail.RequestedShipDate.IsZero() {
		days := core.DaysUntil(detail.RequestedShipDate, now)
		switch {
		case days < 0:
			return sprintf("%s — %d days past the ship date", detail.Status.Label(), -days)
		case days == 0:
			return detail.Status.Label() + " — ships today"
		case days <= 14:
			return sprintf("%s — ships in %d days", detail.Status.Label(), days)
		}
	}
	return detail.Status.Label()
}

func (p *orderDetailPanel) statusImportance(detail core.OrderDetail, now time.Time) widget.Importance {
	if detail.Status.Open() && !detail.CancelAfterDate.IsZero() &&
		detail.CancelAfterDate.Before(now) {
		return widget.DangerImportance
	}
	switch detail.Status {
	case core.OrderShipped:
		return widget.SuccessImportance
	case core.OrderDraft:
		return widget.WarningImportance
	case core.OrderCancelled, core.OrderClosed:
		return widget.LowImportance
	}
	return widget.MediumImportance
}

func (p *orderDetailPanel) renderActions(detail core.OrderDetail) {
	p.actionRow.RemoveAll()

	edit := widget.NewButtonWithIcon("Edit", theme.DocumentCreateIcon(), func() {
		p.app.openOrderByID(detail.ID)
	})

	var primary *widget.Button
	switch detail.Status {
	case core.OrderDraft:
		primary = widget.NewButtonWithIcon("Confirm to client", theme.ConfirmIcon(), func() {
			p.app.confirmOrder(detail)
		})
		primary.Importance = widget.HighImportance
	case core.OrderCancelled, core.OrderClosed:
		primary = widget.NewButtonWithIcon("Reopen", theme.ViewRestoreIcon(), func() {
			p.app.reopenOrder(detail)
		})
	default:
		primary = widget.NewButtonWithIcon("Back to draft", theme.MailReplyIcon(), func() {
			p.app.revertOrderToDraft(detail)
		})
	}

	var secondary *widget.Button
	if detail.Status.Open() && detail.Totals.Shipped == 0 {
		secondary = widget.NewButtonWithIcon("Cancel order", theme.CancelIcon(), func() {
			p.app.cancelOrder(detail)
		})
	} else {
		secondary = widget.NewButtonWithIcon("Copy details", theme.ContentCopyIcon(), func() {
			p.app.copyOrderSummary(detail)
		})
	}

	p.actionRow.Add(container.NewGridWithColumns(3, primary, edit, secondary))
	p.actionRow.Refresh()
}

func (p *orderDetailPanel) renderSchedule(detail core.OrderDetail, now time.Time) {
	p.scheduleBox.RemoveAll()

	add := func(name, value string, importance widget.Importance) {
		label := widget.NewLabel(value)
		label.Importance = importance
		label.Wrapping = fyne.TextWrapWord
		formRow(p.scheduleBox, name, label)
	}

	add("Ordered", formatDate(detail.OrderedAt), widget.MediumImportance)

	shipImportance := widget.MediumImportance
	shipText := formatDate(detail.RequestedShipDate)
	if !detail.RequestedShipDate.IsZero() && detail.Status.Open() {
		days := core.DaysUntil(detail.RequestedShipDate, now)
		shipText += sprintf("   (%s)", describeDayGap(days))
		if days < 0 {
			shipImportance = widget.DangerImportance
		} else if days <= 7 {
			shipImportance = widget.WarningImportance
		}
	}
	add("Requested ship", shipText, shipImportance)

	cancelImportance := widget.MediumImportance
	cancelText := formatDate(detail.CancelAfterDate)
	if !detail.CancelAfterDate.IsZero() && detail.Status.Open() {
		days := core.DaysUntil(detail.CancelAfterDate, now)
		cancelText += sprintf("   (%s)", describeDayGap(days))
		if days < 0 {
			cancelImportance = widget.DangerImportance
		}
	}
	add("Cancel after", cancelText, cancelImportance)

	if detail.Program != nil {
		add("Program", detail.Program.Code+" — "+detail.Program.Name, widget.MediumImportance)
		add("Program status", detail.Program.Status.Label(), widget.LowImportance)
	}
	if detail.Notes != "" {
		add("Notes", detail.Notes, widget.MediumImportance)
	}

	p.scheduleBox.Refresh()
}

func (p *orderDetailPanel) renderShipTo(detail core.OrderDetail) {
	p.shipToBox.RemoveAll()
	p.shipToBox.Add(sectionHeading("Ship to"))

	address := detail.Store.ShipTo
	if address.IsEmpty() {
		warning := widget.NewLabel("No address on file for this store — the delivery paperwork will be incomplete.")
		warning.Importance = widget.WarningImportance
		warning.Wrapping = fyne.TextWrapWord
		p.shipToBox.Add(warning)
	} else {
		block := widget.NewLabel(detail.Store.Name + "\n" + joinLines(address.Lines()))
		block.Wrapping = fyne.TextWrapWord
		block.Selectable = true // shipping paperwork gets copied out
		p.shipToBox.Add(block)
	}

	if !detail.Store.Contact.IsEmpty() {
		contact := widget.NewLabel(describeContact(detail.Store.Contact))
		contact.Importance = widget.LowImportance
		contact.Wrapping = fyne.TextWrapWord
		p.shipToBox.Add(contact)
	}
	p.shipToBox.Refresh()

	// Routing notes are the customer's delivery requirements. Retailers charge
	// back for getting them wrong, so they are given their own emphasis rather
	// than being buried with the address.
	p.routingBox.RemoveAll()
	if detail.Store.RoutingNotes != "" {
		p.routingBox.Add(sectionHeading("Routing requirements"))
		notes := widget.NewLabel(detail.Store.RoutingNotes)
		notes.Importance = widget.WarningImportance
		notes.Wrapping = fyne.TextWrapWord
		notes.Selectable = true
		p.routingBox.Add(notes)
	}
	p.routingBox.Refresh()
}

func (p *orderDetailPanel) renderLines(detail core.OrderDetail) {
	p.linesBox.RemoveAll()

	for _, line := range detail.Lines {
		p.linesBox.Add(orderLineRow(line))
	}
	if len(detail.Lines) == 0 {
		note := widget.NewLabel("This order has no lines.")
		note.Importance = widget.WarningImportance
		p.linesBox.Add(note)
	}
	p.linesBox.Refresh()
}

// orderLineRow renders one line with the fact a picker needs most: whether
// there is enough stock to send it.
func orderLineRow(line core.OrderLineDetail) fyne.CanvasObject {
	name := widget.NewLabel(line.SKU + " — " + line.Name)
	name.Truncation = fyne.TextTruncateEllipsis

	quantity := widget.NewLabel(sprintf("%s %s", formatQuantity(line.Quantity), line.Unit))
	quantity.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
	quantity.Alignment = fyne.TextAlignTrailing

	status := widget.NewLabel(describeLineCoverage(line))
	status.Importance = lineCoverageImportance(line)
	status.Alignment = fyne.TextAlignTrailing
	status.SizeName = theme.SizeNameCaptionText

	value := widget.NewLabel(line.LineTotal().Display())
	value.Alignment = fyne.TextAlignTrailing
	value.Importance = widget.LowImportance
	value.SizeName = theme.SizeNameCaptionText

	right := container.NewVBox(
		container.NewGridWrap(fyne.NewSize(150, 26), quantity),
		container.NewGridWrap(fyne.NewSize(150, 20), status),
	)
	left := container.NewVBox(name, value)

	return container.NewBorder(nil, nil, nil, right, left)
}

// describeLineCoverage says whether the line can actually go.
func describeLineCoverage(line core.OrderLineDetail) string {
	outstanding := line.Outstanding()
	if outstanding == 0 {
		if line.ShippedQty > 0 {
			return "shipped in full"
		}
		return "nothing outstanding"
	}
	if line.ShippedQty > 0 {
		return sprintf("%s shipped, %s to go", formatQuantity(line.ShippedQty), formatQuantity(outstanding))
	}
	if line.OnHand < outstanding {
		return sprintf("short %s — %s on hand",
			formatQuantity(outstanding-line.OnHand), formatQuantity(line.OnHand))
	}
	return sprintf("%s on hand", formatQuantity(line.OnHand))
}

func lineCoverageImportance(line core.OrderLineDetail) widget.Importance {
	outstanding := line.Outstanding()
	if outstanding == 0 {
		return widget.SuccessImportance
	}
	if line.OnHand < outstanding {
		return widget.DangerImportance
	}
	return widget.LowImportance
}

func (p *orderDetailPanel) renderTotals(detail core.OrderDetail) {
	p.totalsBox.RemoveAll()

	add := func(name, value string, bold bool) {
		label := widget.NewLabel(value)
		label.TextStyle = fyne.TextStyle{Bold: bold, Monospace: true}
		formRow(p.totalsBox, name, label)
	}

	add("Lines", formatQuantity(int64(detail.Totals.Lines)), false)
	add("Units ordered", formatQuantity(detail.Totals.Units), false)
	add("Shipped", formatQuantity(detail.Totals.Shipped), false)
	if detail.Totals.Cancelled > 0 {
		add("Cancelled", formatQuantity(detail.Totals.Cancelled), false)
	}
	add("Outstanding", formatQuantity(detail.Totals.Outstanding), true)
	add("Order value", detail.Totals.Value.Display(), true)

	p.totalsBox.Refresh()
}

// describeDayGap renders a day count relative to today.
func describeDayGap(days int) string {
	switch {
	case days < -1:
		return sprintf("%d days ago", -days)
	case days == -1:
		return "yesterday"
	case days == 0:
		return "today"
	case days == 1:
		return "tomorrow"
	}
	return sprintf("in %d days", days)
}

func joinLines(lines []string) string {
	out := ""
	for i, line := range lines {
		if i > 0 {
			out += "\n"
		}
		out += line
	}
	return out
}

func describeContact(c core.Contact) string {
	parts := make([]string, 0, 3)
	for _, part := range []string{c.Name, c.Email, c.Phone} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += "  ·  "
		}
		out += part
	}
	return out
}
