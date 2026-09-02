package ui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/rohankewalramani/inventory-sys/internal/service"
	appstorage "github.com/rohankewalramani/inventory-sys/internal/storage"
)

// --- navigation helpers used by the dashboard -------------------------------

func (a *App) showReorderList() {
	if reports, ok := a.views[viewReports].(*reportsView); ok {
		a.show(viewReports)
		reports.showTab(reportReorder)
	}
}

func (a *App) showValuationReport() {
	if reports, ok := a.views[viewReports].(*reportsView); ok {
		a.show(viewReports)
		reports.showTab(reportValuation)
	}
}

// showLateOrders opens the orders screen scoped to what is past its cancel date.
func (a *App) showLateOrders() {
	orders, ok := a.views[viewOrders].(*ordersView)
	if !ok {
		return
	}
	a.show(viewOrders)
	for i, scope := range orderScopes {
		if scope.label == "Late — past cancel date" {
			orders.scopeSel.SetSelectedIndex(i)
			return
		}
	}
}

func (a *App) showCoverageReport() {
	if reports, ok := a.views[viewReports].(*reportsView); ok {
		a.show(viewReports)
		reports.showTab(reportCoverage)
	}
}

func (a *App) showExpiryReport() {
	if reports, ok := a.views[viewReports].(*reportsView); ok {
		a.show(viewReports)
		reports.showTab(reportExpiry)
	}
}

// showStockState opens the catalogue filtered to one stock condition.
func (a *App) showStockState(state appstorage.StockState) {
	products, ok := a.views[viewProducts].(*productsView)
	if !ok {
		return
	}
	a.show(viewProducts)

	for i, option := range stockFilterOptions {
		if option.state == state {
			products.stockSel.SetSelectedIndex(i)
			return
		}
	}
}

// --- CSV import -------------------------------------------------------------

// openImportDialog runs a CSV import, always previewing before it writes.
//
// The preview is not optional. An import that silently rewrites a catalogue is
// the single most destructive thing this application can do, and the only
// honest way to offer it is to show exactly what will change first.
func (a *App) openImportDialog() {
	picker := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			a.showError(err)
			return
		}
		if reader == nil {
			return // cancelled
		}
		defer func() { _ = reader.Close() }()

		data, err := readAll(reader)
		if err != nil {
			a.showError(wrapf(err, "the file could not be read"))
			return
		}
		a.previewImport(reader.URI().Name(), data)
	}, a.window)

	picker.SetFilter(storage.NewExtensionFileFilter([]string{".csv", ".txt"}))
	picker.Resize(fyne.NewSize(820, 560))
	picker.Show()
}

func (a *App) previewImport(filename string, data []byte) {
	updateExisting := widget.NewCheck(
		"Update products that already have this SKU", nil)

	report := widget.NewLabel("Reading…")
	report.Wrapping = fyne.TextWrapWord

	problems := widget.NewLabel("")
	problems.Wrapping = fyne.TextWrapWord
	problems.TextStyle = fyne.TextStyle{Monospace: true}

	var latest service.ImportResult

	runPreview := func() {
		report.SetText("Checking…")
		problems.SetText("")

		a.background(func(ctx context.Context) {
			result, err := a.svc.ImportCSV(ctx, bytes.NewReader(data), service.ImportOptions{
				DryRun:         true,
				UpdateExisting: updateExisting.Checked,
				LocationID:     a.location,
			})
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
				problems.SetText(describeProblems(result))
			})
		})
	}
	updateExisting.OnChanged = func(bool) { runPreview() }

	body := container.NewBorder(
		container.NewVBox(
			widget.NewLabelWithStyle(filename, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			updateExisting,
			widget.NewSeparator(),
			report,
		),
		nil, nil, nil,
		container.NewVScroll(problems),
	)

	d := dialog.NewCustomConfirm("Import products", "Import", "Cancel", body,
		func(confirmed bool) {
			if !confirmed {
				return
			}
			if latest.Created == 0 && latest.Updated == 0 {
				a.status.warn("Nothing to import — no rows would have been created or updated.")
				return
			}
			a.runImport(data, updateExisting.Checked)
		}, a.window)

	d.Resize(fyne.NewSize(760, 560))
	d.Show()
	runPreview()
}

func (a *App) runImport(data []byte, updateExisting bool) {
	a.background(func(ctx context.Context) {
		result, err := a.svc.ImportCSV(ctx, bytes.NewReader(data), service.ImportOptions{
			UpdateExisting: updateExisting,
			LocationID:     a.location,
		})
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
}

func describeImport(result service.ImportResult) string {
	summary := fmt.Sprintf(
		"%d row(s) read. %d would be created, %d updated, %d skipped.",
		result.Rows, result.Created, result.Updated, result.Skipped)

	if len(result.Mapped) > 0 {
		summary += "\n\nColumns recognised: " + strings.Join(result.Mapped, ", ")
	}
	if len(result.Ignored) > 0 {
		summary += "\nColumns ignored: " + strings.Join(result.Ignored, ", ")
	}
	return summary
}

func describeProblems(result service.ImportResult) string {
	if len(result.Problems) == 0 {
		return "No problems found."
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d problem(s):\n\n", len(result.Problems)))
	for _, problem := range result.Problems {
		b.WriteString(problem.Error())
		b.WriteByte('\n')
	}
	return b.String()
}

func readAll(reader fyne.URIReadCloser) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(reader); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// --- CSV export -------------------------------------------------------------

// openExportDialog writes the current catalogue selection to a file.
func (a *App) openExportDialog() {
	filter := appstorage.ProductFilter{Sort: appstorage.SortProductSKU}
	if products, ok := a.views[viewProducts].(*productsView); ok {
		// Exporting what is on screen is almost always what is wanted, and it
		// makes the filter bar double as a report builder.
		filter = products.filter
	}

	save := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil {
			a.showError(err)
			return
		}
		if writer == nil {
			return
		}

		a.background(func(ctx context.Context) {
			count, exportErr := a.svc.ExportCSV(ctx, writer, filter)
			closeErr := writer.Close()

			a.onMain(func() {
				if exportErr != nil {
					a.showError(wrapf(exportErr, "the export failed"))
					return
				}
				if closeErr != nil {
					a.showError(wrapf(closeErr, "the file could not be closed"))
					return
				}
				a.status.success("Exported %s to %s.",
					pluralize(count, "product", "products"), writer.URI().Name())
			})
		})
	}, a.window)

	save.SetFileName(fmt.Sprintf("products-%s.csv", time.Now().Format("2006-01-02")))
	save.Resize(fyne.NewSize(820, 560))
	save.Show()
}

// saveCSVTemplates offers a blank import file for each thing that can be
// imported, so a first import starts from an example rather than guesswork.
func (a *App) saveCSVTemplates() {
	templates := []struct {
		label    string
		filename string
		write    func(io.Writer) error
	}{
		{"Products", "products-template.csv", service.CSVTemplate},
		{"Customer stores", "stores-template.csv", service.StoresCSVTemplate},
		{"Store orders", "orders-template.csv", service.OrdersCSVTemplate},
	}

	buttons := container.NewVBox(wrappedLabel(
		"Each file has the headings this application reads, plus one example row. " +
			"Import matches headings loosely, so a client's own export usually works too."))

	d := dialog.NewCustom("Download a CSV template", "Close", buttons, a.window)
	for _, template := range templates {
		write, filename := template.write, template.filename
		button := widget.NewButton(template.label, func() {
			d.Hide()
			a.saveTemplateFile(filename, write)
		})
		buttons.Add(button)
	}

	d.Resize(fyne.NewSize(520, 320))
	d.Show()
}

func (a *App) saveTemplateFile(filename string, write func(io.Writer) error) {
	save := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil {
			a.showError(err)
			return
		}
		if writer == nil {
			return
		}

		writeErr := write(writer)
		closeErr := writer.Close()
		if writeErr != nil {
			a.showError(wrapf(writeErr, "the template could not be written"))
			return
		}
		if closeErr != nil {
			a.showError(closeErr)
			return
		}
		a.status.success("Template saved to %s.", writer.URI().Name())
	}, a.window)

	save.SetFileName(filename)
	save.Show()
}

// --- backup -----------------------------------------------------------------

// openBackupDialog writes a consistent snapshot of the database.
func (a *App) openBackupDialog() {
	backupper, ok := a.svc.Backupper()
	if !ok {
		a.showInfoDialog("Back up the database", wrappedLabel(
			"This storage backend does not support file backups from inside the application. "+
				"Use the tooling for your database server, such as pg_dump."))
		return
	}

	save := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil {
			a.showError(err)
			return
		}
		if writer == nil {
			return
		}

		// The backend writes the snapshot itself, so the placeholder the file
		// picker created is removed first — SQLite's VACUUM INTO refuses to
		// overwrite an existing file, which is the behaviour that stops a
		// backup silently clobbering a good one.
		path := writer.URI().Path()
		_ = writer.Close()
		_ = os.Remove(path)

		a.background(func(ctx context.Context) {
			backupErr := backupper.BackupTo(ctx, path)
			a.onMain(func() {
				if backupErr != nil {
					a.showError(wrapf(backupErr, "the backup failed"))
					return
				}
				a.status.success("Backed up to %s.", filepath.Base(path))
			})
		})
	}, a.window)

	save.SetFileName(fmt.Sprintf("inventory-backup-%s.db", time.Now().Format("2006-01-02-1504")))
	save.Resize(fyne.NewSize(820, 560))
	save.Show()
}
