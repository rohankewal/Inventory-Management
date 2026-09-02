package ui

import (
	"bytes"
	"context"
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	fynestorage "fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/service"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

// --- order mutations --------------------------------------------------------

func (a *App) saveOrder(order core.StoreOrder, lines []core.StoreOrderLine) {
	creating := order.ID.IsZero()

	a.background(func(ctx context.Context) {
		saved, err := a.svc.SaveOrder(ctx, order, lines)
		a.onMain(func() {
			if err != nil {
				a.showError(err)
				return
			}
			if creating {
				a.status.success("Created %s. Confirm it when you are ready to tell the client.",
					saved.CustomerPONumber)
			} else {
				a.status.success("Saved %s.", saved.CustomerPONumber)
			}
			a.reloadAll()
		})
	})
}

func (a *App) confirmOrder(detail core.OrderDetail) {
	a.setOrderStatus(detail, core.OrderConfirmed,
		sprintf("Confirmed %s to %s.", detail.CustomerPONumber, detail.Customer.Name))
}

func (a *App) revertOrderToDraft(detail core.OrderDetail) {
	a.setOrderStatus(detail, core.OrderDraft,
		sprintf("%s is back to draft.", detail.CustomerPONumber))
}

func (a *App) reopenOrder(detail core.OrderDetail) {
	a.setOrderStatus(detail, core.OrderConfirmed,
		sprintf("Reopened %s.", detail.CustomerPONumber))
}

func (a *App) setOrderStatus(detail core.OrderDetail, status core.OrderStatus, message string) {
	a.background(func(ctx context.Context) {
		err := a.svc.SetOrderStatus(ctx, detail.ID, status)
		a.onMain(func() {
			if err != nil {
				a.showError(err)
				return
			}
			a.status.success("%s", message)
			a.reloadAll()
		})
	})
}

// cancelOrder withdraws an order, confirming first because a client is told
// about it.
func (a *App) cancelOrder(detail core.OrderDetail) {
	message := sprintf(
		"Cancel %s for %s — %s?\n\n%s units on %s will no longer be expected. "+
			"The order stays on file so the history is intact.",
		detail.CustomerPONumber, detail.Customer.Name, detail.Store.Label(),
		formatQuantity(detail.Totals.Units), pluralize(detail.Totals.Lines, "line", "lines"))

	a.confirm("Cancel order", message, "Cancel the order", func() {
		a.background(func(ctx context.Context) {
			err := a.svc.CancelOrder(ctx, detail.ID)
			a.onMain(func() {
				if err != nil {
					a.showError(err)
					return
				}
				a.status.warn("Cancelled %s.", detail.CustomerPONumber)
				a.reloadAll()
			})
		})
	})
}

// copyOrderSummary shows a plain-text summary of the order.
//
// It exists because the most common client question — "what is on this PO and
// when is it coming" — is answered today by retyping the order into an email.
// Proper documents arrive in the next phase; this makes the interim honest.
func (a *App) copyOrderSummary(detail core.OrderDetail) {
	var b bytes.Buffer

	fmt.Fprintf(&b, "PO %s\n", detail.CustomerPONumber)
	fmt.Fprintf(&b, "%s — %s\n", detail.Customer.Name, detail.Store.Label())
	if lines := detail.Store.ShipTo.Lines(); len(lines) > 0 {
		fmt.Fprintf(&b, "%s\n", joinLines(lines))
	}
	fmt.Fprintf(&b, "\nStatus: %s\n", detail.Status.Label())
	if !detail.RequestedShipDate.IsZero() {
		fmt.Fprintf(&b, "Requested ship: %s\n", formatDate(detail.RequestedShipDate))
	}
	if !detail.CancelAfterDate.IsZero() {
		fmt.Fprintf(&b, "Cancel after:   %s\n", formatDate(detail.CancelAfterDate))
	}
	if detail.Program != nil {
		fmt.Fprintf(&b, "Program:        %s — %s\n", detail.Program.Code, detail.Program.Name)
	}

	b.WriteString("\nLines\n")
	for _, line := range detail.Lines {
		fmt.Fprintf(&b, "  %-16s %-32s %8s %s  %12s\n",
			line.SKU, truncate(line.Name, 32),
			formatQuantity(line.Quantity), line.Unit, line.LineTotal().Display())
		if line.ShippedQty > 0 {
			fmt.Fprintf(&b, "  %-16s   shipped %s, %s outstanding\n", "",
				formatQuantity(line.ShippedQty), formatQuantity(line.Outstanding()))
		}
	}
	fmt.Fprintf(&b, "\n%s units  ·  %s\n",
		formatQuantity(detail.Totals.Units), detail.Totals.Value.Display())

	body := widget.NewMultiLineEntry()
	body.SetText(b.String())
	body.TextStyle = fyne.TextStyle{Monospace: true}
	body.Wrapping = fyne.TextWrapOff

	copyButton := widget.NewButton("Copy to clipboard", func() {
		a.window.Clipboard().SetContent(b.String())
		a.status.success("Copied %s to the clipboard.", detail.CustomerPONumber)
	})

	content := container.NewBorder(nil, copyButton, nil, nil, body)
	d := dialog.NewCustom("Order summary", "Close", content, a.window)
	d.Resize(fyne.NewSize(760, 560))
	d.Show()
}

// showOrdersForStore opens the orders screen filtered to one door.
func (a *App) showOrdersForStore(store core.CustomerStore) {
	orders, ok := a.views[viewOrders].(*ordersView)
	if !ok {
		return
	}
	a.show(viewOrders)
	orders.setSearch(store.Code)
}

// showOrder brings one order on screen and selects it.
func (a *App) showOrder(summary core.OrderSummary) {
	orders, ok := a.views[viewOrders].(*ordersView)
	if !ok {
		return
	}
	a.show(viewOrders)
	orders.setSearch(summary.CustomerPONumber)
}

// --- imports ----------------------------------------------------------------

// openStoreImportDialog loads a customer's store list from a spreadsheet.
func (a *App) openStoreImportDialog(customer core.Customer) {
	a.importCSVDialog(csvImportSpec{
		title:       "Import stores for " + customer.Name,
		subtitle:    "A spreadsheet of the client's doors. Headings are matched loosely, so a file exported from their system usually works unchanged.",
		updateLabel: "Update stores that already have this code",
		run: func(ctx context.Context, data []byte, update, dryRun bool) (service.ImportResult, error) {
			return a.svc.ImportStoresCSV(ctx, customer.ID, bytes.NewReader(data), service.ImportOptions{
				DryRun: dryRun, UpdateExisting: update,
			})
		},
	})
}

// openOrderImportDialog loads store POs from a spreadsheet.
//
// The customer is chosen first because PO numbers are only unique within one,
// and because the store codes in the file are theirs.
func (a *App) openOrderImportDialog() {
	a.background(func(ctx context.Context) {
		page, err := a.svc.ListCustomers(ctx, storage.CustomerFilter{})
		a.onMain(func() {
			if err != nil {
				a.showError(err)
				return
			}
			if len(page.Items) == 0 {
				a.showInfoDialog("Import orders", wrappedLabel(
					"There are no customers yet. Add the client and import their store "+
						"list before importing orders."))
				return
			}
			a.chooseCustomerThenImport(page.Items)
		})
	})
}

func (a *App) chooseCustomerThenImport(customers []core.CustomerWithStores) {
	names := make([]string, len(customers))
	for i, c := range customers {
		names[i] = sprintf("%s — %s", c.Code, c.Name)
	}

	picker := widget.NewSelect(names, nil)
	picker.PlaceHolder = "Choose the customer these orders are from"
	if len(customers) == 1 {
		picker.SetSelectedIndex(0)
	}

	body := container.NewVBox(
		wrappedLabel("PO numbers belong to a customer, and so do the store codes in the file."),
		picker,
	)

	d := dialog.NewCustomConfirm("Import orders", "Continue", "Cancel", body, func(ok bool) {
		if !ok {
			return
		}
		i := picker.SelectedIndex()
		if i < 0 || i >= len(customers) {
			a.status.warn("Choose a customer first.")
			return
		}
		customer := customers[i]

		a.importCSVDialog(csvImportSpec{
			title:       "Import orders for " + customer.Name,
			subtitle:    "One file, many stores. Rows are grouped by PO number, so each store's PO becomes its own order.",
			updateLabel: "Replace the lines of POs that already exist",
			run: func(ctx context.Context, data []byte, update, dryRun bool) (service.ImportResult, error) {
				return a.svc.ImportOrdersCSV(ctx, customer.ID, bytes.NewReader(data), service.ImportOptions{
					DryRun: dryRun, UpdateExisting: update,
				})
			},
		})
	}, a.window)

	d.Resize(fyne.NewSize(520, 240))
	d.Show()
}

// csvImportSpec describes one import flow, so the preview-then-apply dialog is
// written once rather than per entity.
type csvImportSpec struct {
	title       string
	subtitle    string
	updateLabel string
	run         func(ctx context.Context, data []byte, update, dryRun bool) (service.ImportResult, error)
}

// importCSVDialog runs the shared preview-then-apply flow.
//
// The preview is not optional for any import. An import that rewrites a
// client's orders without showing what will change first is the most
// destructive thing this application can do.
func (a *App) importCSVDialog(spec csvImportSpec) {
	picker := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			a.showError(err)
			return
		}
		if reader == nil {
			return
		}
		defer func() { _ = reader.Close() }()

		data, err := readAll(reader)
		if err != nil {
			a.showError(wrapf(err, "the file could not be read"))
			return
		}
		a.previewCSVImport(spec, reader.URI().Name(), data)
	}, a.window)

	picker.SetFilter(fynestorage.NewExtensionFileFilter([]string{".csv", ".txt"}))
	picker.Resize(fyne.NewSize(820, 560))
	picker.Show()
}

func (a *App) previewCSVImport(spec csvImportSpec, filename string, data []byte) {
	update := widget.NewCheck(spec.updateLabel, nil)

	report := widget.NewLabel("Checking…")
	report.Wrapping = fyne.TextWrapWord

	problems := widget.NewLabel("")
	problems.Wrapping = fyne.TextWrapWord
	problems.TextStyle = fyne.TextStyle{Monospace: true}

	var latest service.ImportResult

	preview := func() {
		report.SetText("Checking…")
		report.Importance = widget.MediumImportance
		problems.SetText("")

		a.background(func(ctx context.Context) {
			result, err := spec.run(ctx, data, update.Checked, true)
			a.onMain(func() {
				if err != nil {
					report.SetText(humanError(err))
					report.Importance = widget.DangerImportance
					report.Refresh()
					return
				}
				latest = result
				report.Importance = widget.MediumImportance
				report.SetText(describeImport(result))
				report.Refresh()
				problems.SetText(describeProblems(result))
			})
		})
	}
	update.OnChanged = func(bool) { preview() }

	subtitle := widget.NewLabel(spec.subtitle)
	subtitle.Wrapping = fyne.TextWrapWord
	subtitle.Importance = widget.LowImportance

	body := container.NewBorder(
		container.NewVBox(
			widget.NewLabelWithStyle(filename, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			subtitle,
			update,
			widget.NewSeparator(),
			report,
		),
		nil, nil, nil,
		container.NewVScroll(problems),
	)

	d := dialog.NewCustomConfirm(spec.title, "Import", "Cancel", body, func(ok bool) {
		if !ok {
			return
		}
		if latest.Created == 0 && latest.Updated == 0 {
			a.status.warn("Nothing to import — no rows would have been created or updated.")
			return
		}

		a.background(func(ctx context.Context) {
			result, err := spec.run(ctx, data, update.Checked, false)
			a.onMain(func() {
				if err != nil {
					a.showError(wrapf(err, "the import failed and nothing was changed"))
					return
				}
				if len(result.Problems) > 0 {
					a.status.warn("%s", result.Summary())
				} else {
					a.status.success("%s", result.Summary())
				}
				a.reloadAll()
			})
		})
	}, a.window)

	d.Resize(fyne.NewSize(780, 580))
	d.Show()
	preview()
}
