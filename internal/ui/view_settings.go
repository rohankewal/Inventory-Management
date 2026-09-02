package ui

import (
	"context"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/rohankewalramani/inventory-sys/internal/config"
	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

// settingsView holds the settings that belong to the database and the ones
// that belong to this machine, kept visually apart because the first affect
// everyone on a shared install and the second affect nobody else.
type settingsView struct {
	app *App

	content fyne.CanvasObject

	companyEntry  *widget.Entry
	valuationSel  *widget.Select
	appearanceSel *widget.Select
	locationLabel *widget.Label
	schemaLabel   *widget.Label
}

func newSettingsView(a *App) *settingsView {
	v := &settingsView{app: a}
	v.build()
	return v
}

func (v *settingsView) title() string { return "Settings" }

func (v *settingsView) object() fyne.CanvasObject { return v.content }

func (v *settingsView) actions() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		toolbarButton("Back up now", theme.DownloadIcon(), widget.MediumImportance, v.app.openBackupDialog),
	}
}

func (v *settingsView) build() {
	v.companyEntry = newEntry("Your business name")
	v.companyEntry.OnSubmitted = func(name string) { v.saveCompany(name) }

	methodLabels := make([]string, len(core.ValuationMethods))
	for i, m := range core.ValuationMethods {
		methodLabels[i] = m.Label()
	}
	v.valuationSel = widget.NewSelect(methodLabels, func(string) { v.saveValuationMethod() })

	appearanceLabels := make([]string, len(Appearances))
	for i, a := range Appearances {
		appearanceLabels[i] = a.Label()
	}
	v.appearanceSel = widget.NewSelect(appearanceLabels, nil)
	v.appearanceSel.OnChanged = func(string) {
		i := v.appearanceSel.SelectedIndex()
		if i >= 0 && i < len(Appearances) {
			v.app.setAppearance(Appearances[i])
		}
	}

	v.locationLabel = widget.NewLabel("")
	v.schemaLabel = widget.NewLabel("")

	company := container.New(newFormGrid())
	formRow(company, "Business name", v.companyEntry)
	formRow(company, "Currency", widget.NewLabel(string(v.app.currency)+
		"  —  set in config.json or the INVENTORY_CURRENCY environment variable"))

	accounting := container.New(newFormGrid())
	formRow(accounting, "Valuation method", v.valuationSel)

	valuationNote := widget.NewLabel(
		"The valuation method is an accounting policy, so it is stored in the database and " +
			"applies to everyone using it. Changing it re-values history rather than only " +
			"affecting stock received afterwards, so pick one and apply it consistently.")
	valuationNote.Importance = widget.LowImportance
	valuationNote.Wrapping = fyne.TextWrapWord

	machine := container.New(newFormGrid())
	formRow(machine, "Appearance", v.appearanceSel)

	storageGrid := container.New(newFormGrid())
	formRow(storageGrid, "Storage", widget.NewLabel(v.app.cfg.Driver))
	formRow(storageGrid, "Database", selectableLabel(v.app.cfg.DatabasePath()))
	formRow(storageGrid, "Data folder", selectableLabel(v.app.cfg.Dir()))
	formRow(storageGrid, "Logs", selectableLabel(v.app.cfg.LogDir()))
	formRow(storageGrid, "Location", v.locationLabel)
	formRow(storageGrid, "Schema", v.schemaLabel)

	maintenance := container.NewHBox(
		widget.NewButtonWithIcon("Back up the database", theme.DownloadIcon(), v.app.openBackupDialog),
		widget.NewButtonWithIcon("Download CSV templates", theme.DocumentIcon(), v.app.saveCSVTemplates),
	)

	body := container.NewVBox(
		sectionHeading("Business"),
		company,
		sectionHeading("Accounting"),
		accounting,
		valuationNote,
		sectionHeading("This computer"),
		machine,
		sectionHeading("Storage"),
		storageGrid,
		maintenance,
	)

	v.content = container.NewVScroll(container.NewPadded(body))
}

func selectableLabel(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.Selectable = true // support requests need copyable paths
	label.Wrapping = fyne.TextWrapWord
	return label
}

func (v *settingsView) reload() {
	// The machine-local preference does not need a query.
	saved := v.app.savedAppearance()
	for i, a := range Appearances {
		if a == saved {
			v.appearanceSel.SetSelectedIndex(i)
			break
		}
	}

	v.app.background(func(ctx context.Context) {
		company, _ := v.app.svc.Setting(ctx, storage.SettingCompanyName, "")
		method, methodErr := v.app.svc.ValuationMethod(ctx)
		location, locationErr := v.app.svc.DefaultLocation(ctx)

		v.app.onMain(func() {
			v.companyEntry.SetText(company)

			if methodErr == nil {
				for i, m := range core.ValuationMethods {
					if m == method {
						v.valuationSel.SetSelectedIndex(i)
						break
					}
				}
			}

			if locationErr == nil {
				v.locationLabel.SetText(location.Name + "  (" + location.Code + ")")
			} else {
				v.locationLabel.SetText("—")
			}

			v.schemaLabel.SetText(v.schemaSummary())
		})
	})
}

func (v *settingsView) schemaSummary() string {
	if v.app.cfg.Driver == config.DriverSQLite {
		return "Migrated automatically when the application starts"
	}
	return "Run `inventoryctl migrate` after upgrading"
}

func (v *settingsView) saveCompany(name string) {
	v.app.background(func(ctx context.Context) {
		err := v.app.svc.SetSetting(ctx, storage.SettingCompanyName, name)
		v.app.onMain(func() {
			if err != nil {
				v.app.showError(err)
				return
			}
			v.app.status.success("Business name saved.")
		})
	})
}

func (v *settingsView) saveValuationMethod() {
	i := v.valuationSel.SelectedIndex()
	if i < 0 || i >= len(core.ValuationMethods) {
		return
	}
	method := core.ValuationMethods[i]

	v.app.background(func(ctx context.Context) {
		current, err := v.app.svc.ValuationMethod(ctx)
		if err == nil && current == method {
			return // selecting what is already set is not a change
		}
		err = v.app.svc.SetValuationMethod(ctx, method)

		v.app.onMain(func() {
			if err != nil {
				v.app.showError(err)
				return
			}
			v.app.status.success("Valuation method set to %s. Stock has been re-valued.", method.Label())
		})
	})
}
