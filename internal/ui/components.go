package ui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// statusBar is the strip along the bottom of the window.
//
// A modal dialog for every outcome trains people to dismiss dialogs without
// reading them. Routine results belong somewhere they can be seen and ignored;
// only a question or a destructive confirmation earns an interruption.
type statusBar struct {
	label *widget.Label
	bar   *fyne.Container
	// onMain marshals the clearing timer back onto the UI goroutine.
	onMain func(func())
	// clearTimer removes a message after a while so a stale success notice is
	// never mistaken for a fresh one.
	clearTimer *time.Timer
}

// statusHold is how long a routine message stays before clearing.
const statusHold = 8 * time.Second

func newStatusBar(onMain func(func())) *statusBar {
	label := widget.NewLabel("Ready.")
	label.Truncation = fyne.TextTruncateEllipsis

	if onMain == nil {
		onMain = fyne.Do
	}
	s := &statusBar{label: label, onMain: onMain}
	s.bar = container.NewVBox(widget.NewSeparator(), container.NewPadded(label))
	return s
}

func (s *statusBar) object() fyne.CanvasObject { return s.bar }

func (s *statusBar) info(format string, args ...any) {
	s.set(widget.MediumImportance, format, args...)
}

func (s *statusBar) success(format string, args ...any) {
	s.set(widget.SuccessImportance, format, args...)
}

func (s *statusBar) warn(format string, args ...any) {
	s.set(widget.WarningImportance, format, args...)
}

// failure keeps its message on screen rather than clearing it, because an
// error the user missed is one they will hit again.
func (s *statusBar) failure(format string, args ...any) {
	s.stopTimer()
	s.label.Importance = widget.DangerImportance
	s.label.SetText(fmt.Sprintf(format, args...))
}

func (s *statusBar) set(importance widget.Importance, format string, args ...any) {
	s.stopTimer()
	s.label.Importance = importance
	s.label.SetText(fmt.Sprintf(format, args...))

	s.clearTimer = time.AfterFunc(statusHold, func() {
		s.onMain(func() {
			s.label.Importance = widget.LowImportance
			s.label.SetText("Ready.")
		})
	})
}

func (s *statusBar) stopTimer() {
	if s.clearTimer != nil {
		s.clearTimer.Stop()
		s.clearTimer = nil
	}
}

// statCard is one headline number on the dashboard.
type statCard struct {
	value  *widget.Label
	detail *widget.Label
	card   *widget.Card
}

func newStatCard(title string) *statCard {
	value := widget.NewLabel("—")
	value.TextStyle = fyne.TextStyle{Bold: true}
	value.SizeName = theme.SizeNameHeadingText

	detail := widget.NewLabel("")
	detail.Importance = widget.LowImportance
	detail.SizeName = theme.SizeNameCaptionText
	detail.Wrapping = fyne.TextWrapWord

	c := &statCard{value: value, detail: detail}
	c.card = widget.NewCard(title, "", container.NewVBox(value, detail))
	return c
}

func (c *statCard) object() fyne.CanvasObject { return c.card }

func (c *statCard) set(value, detail string, importance widget.Importance) {
	c.value.Importance = importance
	c.value.SetText(value)
	c.detail.SetText(detail)
}

// labelledValue renders a read-only "name: value" pair.
func labelledValue(name, value string) fyne.CanvasObject {
	label := widget.NewLabel(name)
	label.Importance = widget.LowImportance

	content := widget.NewLabel(value)
	content.Wrapping = fyne.TextWrapWord
	content.Selectable = true // support requests need copyable paths

	return container.NewBorder(nil, nil, container.NewGridWrap(fyne.NewSize(130, 32), label), nil, content)
}

// formGrid lays out label/field pairs in two columns, with the labels sized to
// the widest and the fields taking the remaining width. Fyne's stock form
// layout sizes both columns to their content, which leaves entry boxes ragged.
type formGrid struct {
	labelWidth float32
	gap        float32
}

func newFormGrid() fyne.Layout { return &formGrid{labelWidth: 150, gap: 8} }

func (f *formGrid) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for i := 0; i+1 < len(objects); i += 2 {
		label, field := objects[i], objects[i+1]
		rowHeight := fyne.Max(label.MinSize().Height, field.MinSize().Height)

		label.Resize(fyne.NewSize(f.labelWidth, rowHeight))
		label.Move(fyne.NewPos(0, y))

		fieldWidth := size.Width - f.labelWidth - f.gap
		if fieldWidth < 0 {
			fieldWidth = 0
		}
		field.Resize(fyne.NewSize(fieldWidth, rowHeight))
		field.Move(fyne.NewPos(f.labelWidth+f.gap, y))

		y += rowHeight + f.gap
	}
}

func (f *formGrid) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var height, widest float32
	for i := 0; i+1 < len(objects); i += 2 {
		rowHeight := fyne.Max(objects[i].MinSize().Height, objects[i+1].MinSize().Height)
		height += rowHeight + f.gap
		widest = fyne.Max(widest, objects[i+1].MinSize().Width)
	}
	return fyne.NewSize(f.labelWidth+f.gap+fyne.Max(widest, 240), height)
}

// formRow adds a labelled field to a form grid.
func formRow(grid *fyne.Container, name string, field fyne.CanvasObject) {
	label := widget.NewLabel(name)
	label.Alignment = fyne.TextAlignTrailing
	grid.Add(label)
	grid.Add(field)
}

// sectionHeading introduces a group of fields.
func sectionHeading(text string) fyne.CanvasObject {
	label := widget.NewLabel(text)
	label.TextStyle = fyne.TextStyle{Bold: true}
	label.SizeName = theme.SizeNameSubHeadingText
	return container.NewVBox(widget.NewSeparator(), label)
}

// showInfoDialog presents read-only content at a readable size. Fyne's stock
// information dialog only takes a string.
func (a *App) showInfoDialog(title string, content fyne.CanvasObject) {
	d := dialog.NewCustom(title, "Close", container.NewVScroll(container.NewPadded(content)), a.window)
	d.Resize(fyne.NewSize(620, 460))
	d.Show()
}

// confirm asks before doing something that cannot be undone. The message says
// what will happen rather than asking "are you sure?" about an unnamed thing.
func (a *App) confirm(title, message, confirmLabel string, action func()) {
	d := dialog.NewCustomConfirm(title, confirmLabel, "Cancel",
		container.NewPadded(wrappedLabel(message)),
		func(ok bool) {
			if ok {
				action()
			}
		}, a.window)
	d.Resize(fyne.NewSize(480, 220))
	d.Show()
}

func wrappedLabel(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	return label
}

// toolbarButton builds a header action.
func toolbarButton(label string, icon fyne.Resource, importance widget.Importance, action func()) *widget.Button {
	button := widget.NewButtonWithIcon(label, icon, action)
	button.Importance = importance
	return button
}
