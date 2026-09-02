package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

// Column describes one column of a DataTable.
type Column[T any] struct {
	Title string
	// Width is in the same units as the rest of Fyne's layout, roughly pixels
	// at the default scale.
	Width float32
	Align fyne.TextAlign
	// SortKey makes the header clickable. Empty means the column cannot be
	// sorted, which is correct for anything computed per row rather than in
	// the query.
	SortKey string
	// Value renders the cell.
	Value func(T) string
	// Importance optionally colours the cell — how a low-stock quantity turns
	// amber and a negative one turns red without every view re-deciding what
	// those colours mean.
	Importance func(T) widget.Importance
	// Bold optionally emphasises the cell.
	Bold func(T) bool
	// Monospace renders the cell in a fixed-width font, which is what makes a
	// column of numbers line up on the decimal point.
	Monospace bool
}

// DataTable is a sortable, selectable table over a slice of rows.
//
// Fyne's Table is a virtualised grid and nothing more: it has no notion of a
// sortable header, a selected record, or an empty state. Those are exactly the
// things every list screen in this application needs, so they are built once
// here rather than four times across the views.
type DataTable[T any] struct {
	columns []Column[T]
	rows    []T

	table     *widget.Table
	empty     *widget.Label
	container *fyne.Container

	sortKey  string
	sortDesc bool

	// OnSortChanged is called when a header is clicked. The view re-queries;
	// sorting is done by the database, not in memory, because the table only
	// ever holds one page of what may be a very large catalogue.
	OnSortChanged func(key string, descending bool)
	// OnSelect is called when a row is chosen.
	OnSelect func(row T, index int)
	// OnActivate is called when a selected row is activated with Enter.
	OnActivate func(row T, index int)

	selected int
}

// NewDataTable builds a table over the given columns.
func NewDataTable[T any](columns []Column[T]) *DataTable[T] {
	d := &DataTable[T]{columns: columns, selected: -1}

	d.table = widget.NewTableWithHeaders(
		func() (int, int) { return len(d.rows), len(d.columns) },
		func() fyne.CanvasObject {
			cell := widget.NewLabel("")
			cell.Truncation = fyne.TextTruncateEllipsis
			return cell
		},
		d.updateCell,
	)
	d.table.ShowHeaderColumn = false
	d.table.CreateHeader = func() fyne.CanvasObject {
		header := widget.NewButton("", nil)
		header.Importance = widget.LowImportance
		header.Alignment = widget.ButtonAlignLeading
		return header
	}
	d.table.UpdateHeader = d.updateHeader
	d.table.OnSelected = d.onSelected

	for i, col := range d.columns {
		d.table.SetColumnWidth(i, col.Width)
	}

	d.empty = widget.NewLabel("")
	d.empty.Alignment = fyne.TextAlignCenter
	d.empty.Importance = widget.LowImportance
	d.empty.Hide()

	d.container = fyne.NewContainerWithoutLayout()
	d.container.Layout = &stackLayout{}
	d.container.Add(d.table)
	d.container.Add(d.empty)

	return d
}

// Object returns the widget to place in a layout.
func (d *DataTable[T]) Object() fyne.CanvasObject { return d.container }

// SetRows replaces the table's contents. emptyMessage is shown in place of the
// grid when there is nothing to display, because a blank rectangle leaves the
// user unable to tell a working filter from a broken screen.
func (d *DataTable[T]) SetRows(rows []T, emptyMessage string) {
	d.rows = rows
	d.selected = -1
	d.table.UnselectAll()

	if len(rows) == 0 {
		d.empty.SetText(emptyMessage)
		d.empty.Show()
		d.table.Hide()
	} else {
		d.empty.Hide()
		d.table.Show()
	}

	d.table.Refresh()
	d.table.ScrollToTop()
}

// Rows returns the current contents.
func (d *DataTable[T]) Rows() []T { return d.rows }

// SetSort records the active sort so the header can show its direction. It
// does not re-query; the owning view does that.
func (d *DataTable[T]) SetSort(key string, descending bool) {
	d.sortKey, d.sortDesc = key, descending
	d.table.Refresh()
}

// Selected returns the selected row and whether there is one.
func (d *DataTable[T]) Selected() (T, bool) {
	var zero T
	if d.selected < 0 || d.selected >= len(d.rows) {
		return zero, false
	}
	return d.rows[d.selected], true
}

// SelectIndex selects a row programmatically.
func (d *DataTable[T]) SelectIndex(i int) {
	if i < 0 || i >= len(d.rows) {
		return
	}
	d.table.Select(widget.TableCellID{Row: i, Col: 0})
}

// ActivateSelection fires OnActivate for the current selection, which is how
// Enter opens the highlighted record.
func (d *DataTable[T]) ActivateSelection() {
	row, ok := d.Selected()
	if ok && d.OnActivate != nil {
		d.OnActivate(row, d.selected)
	}
}

// Refresh redraws the visible cells without changing the data, for when a row's
// derived display has changed.
func (d *DataTable[T]) Refresh() { d.table.Refresh() }

func (d *DataTable[T]) updateCell(id widget.TableCellID, template fyne.CanvasObject) {
	cell, ok := template.(*widget.Label)
	if !ok || id.Row < 0 || id.Row >= len(d.rows) || id.Col < 0 || id.Col >= len(d.columns) {
		return
	}

	col := d.columns[id.Col]
	row := d.rows[id.Row]

	cell.Text = col.Value(row)
	cell.Alignment = col.Align

	cell.Importance = widget.MediumImportance
	if col.Importance != nil {
		cell.Importance = col.Importance(row)
	}

	style := fyne.TextStyle{Monospace: col.Monospace}
	if col.Bold != nil {
		style.Bold = col.Bold(row)
	}
	cell.TextStyle = style

	cell.Refresh()
}

func (d *DataTable[T]) updateHeader(id widget.TableCellID, template fyne.CanvasObject) {
	header, ok := template.(*widget.Button)
	if !ok || id.Col < 0 || id.Col >= len(d.columns) {
		return
	}

	col := d.columns[id.Col]
	label := col.Title
	if col.SortKey != "" && col.SortKey == d.sortKey {
		// An arrow beside the active column is the only affordance telling a
		// user what the order they are looking at actually is.
		if d.sortDesc {
			label += "  ↓"
		} else {
			label += "  ↑"
		}
	}
	header.SetText(label)

	if col.SortKey == "" {
		header.OnTapped = nil
		header.Disable()
		return
	}
	header.Enable()
	header.OnTapped = func() { d.toggleSort(col.SortKey) }
}

// toggleSort switches to a column, or reverses it if it is already active.
func (d *DataTable[T]) toggleSort(key string) {
	if d.sortKey == key {
		d.sortDesc = !d.sortDesc
	} else {
		d.sortKey, d.sortDesc = key, false
	}

	d.table.Refresh()
	if d.OnSortChanged != nil {
		d.OnSortChanged(d.sortKey, d.sortDesc)
	}
}

func (d *DataTable[T]) onSelected(id widget.TableCellID) {
	if id.Row < 0 || id.Row >= len(d.rows) {
		return
	}
	d.selected = id.Row
	if d.OnSelect != nil {
		d.OnSelect(d.rows[id.Row], id.Row)
	}
}

// stackLayout fills the container with every visible child, which is how the
// grid and its empty-state message occupy the same space.
type stackLayout struct{}

func (stackLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		o.Resize(size)
		o.Move(fyne.NewPos(0, 0))
	}
}

func (stackLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var min fyne.Size
	for _, o := range objects {
		min = min.Max(o.MinSize())
	}
	return min
}

// sortDirection converts the table's boolean into the storage enum.
func sortDirection(descending bool) storage.SortDirection {
	if descending {
		return storage.Descending
	}
	return storage.Ascending
}
