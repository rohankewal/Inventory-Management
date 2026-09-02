package ui

import (
	"context"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/rohankewalramani/inventory-sys/internal/core"
)

// openCustomerForm creates or edits a client business. Pass nil to create.
func (a *App) openCustomerForm(existing *core.Customer) {
	code := newEntry("Short reference, e.g. MACYS")
	name := newEntry("Required")
	currency := newEntry(string(a.currency))
	terms := newEntry("e.g. Net 60")

	contactName := newEntry("Buying office contact")
	contactEmail := newEntry("name@example.com")
	contactPhone := newEntry("Phone")

	line1 := newEntry("Head office address")
	line2 := newEntry("")
	city := newEntry("City")
	region := newEntry("State or region")
	postal := newEntry("Postal code")
	country := newEntry("Country")

	notes := widget.NewMultiLineEntry()
	notes.SetPlaceHolder("Anything worth recording about this client")
	notes.Wrapping = fyne.TextWrapWord

	if existing != nil {
		code.SetText(existing.Code)
		name.SetText(existing.Name)
		currency.SetText(string(existing.Currency))
		terms.SetText(existing.Terms)
		contactName.SetText(existing.Contact.Name)
		contactEmail.SetText(existing.Contact.Email)
		contactPhone.SetText(existing.Contact.Phone)
		line1.SetText(existing.BillTo.Line1)
		line2.SetText(existing.BillTo.Line2)
		city.SetText(existing.BillTo.City)
		region.SetText(existing.BillTo.Region)
		postal.SetText(existing.BillTo.PostalCode)
		country.SetText(existing.BillTo.Country)
		notes.SetText(existing.Notes)
	}

	identity := container.New(newFormGrid())
	formRow(identity, "Code", code)
	formRow(identity, "Name", name)
	formRow(identity, "Currency", currency)
	formRow(identity, "Payment terms", terms)

	contact := container.New(newFormGrid())
	formRow(contact, "Contact name", contactName)
	formRow(contact, "Email", contactEmail)
	formRow(contact, "Phone", contactPhone)

	billTo := container.New(newFormGrid())
	formRow(billTo, "Address line 1", line1)
	formRow(billTo, "Address line 2", line2)
	formRow(billTo, "City", city)
	formRow(billTo, "State or region", region)
	formRow(billTo, "Postal code", postal)
	formRow(billTo, "Country", country)

	body := container.NewVScroll(container.NewPadded(container.NewVBox(
		identity,
		sectionHeading("Buying office contact"),
		contact,
		sectionHeading("Bill to"),
		billTo,
		sectionHeading("Notes"),
		notes,
	)))

	title, confirm := "New customer", "Create"
	if existing != nil {
		title, confirm = "Edit "+existing.Code, "Save changes"
	}

	submit := func(ok bool) {
		if !ok {
			return
		}

		customer := core.Customer{
			Code:     strings.TrimSpace(code.Text),
			Name:     strings.TrimSpace(name.Text),
			Currency: core.Currency(strings.TrimSpace(currency.Text)),
			Terms:    strings.TrimSpace(terms.Text),
			Contact: core.Contact{
				Name: contactName.Text, Email: contactEmail.Text, Phone: contactPhone.Text,
			},
			BillTo: core.Address{
				Line1: line1.Text, Line2: line2.Text, City: city.Text,
				Region: region.Text, PostalCode: postal.Text, Country: country.Text,
			},
			Notes:  notes.Text,
			Active: true,
		}
		if existing != nil {
			customer.ID = existing.ID
			customer.Version = existing.Version
			customer.CreatedAt = existing.CreatedAt
			customer.Active = existing.Active
		}
		a.saveCustomer(customer, existing == nil)
	}

	d := dialog.NewCustomConfirm(title, confirm, "Cancel", body, submit, a.window)
	d.Resize(fyne.NewSize(680, 620))
	d.Show()
	a.window.Canvas().Focus(code)
}

func (a *App) saveCustomer(customer core.Customer, creating bool) {
	a.background(func(ctx context.Context) {
		var err error
		if creating {
			customer, err = a.svc.CreateCustomer(ctx, customer)
		} else {
			customer, err = a.svc.UpdateCustomer(ctx, customer)
		}

		a.onMain(func() {
			if err != nil {
				a.showError(err)
				return
			}
			if creating {
				a.status.success("Created %s. Add its stores next.", customer.Name)
			} else {
				a.status.success("Saved %s.", customer.Name)
			}
			a.reloadAll()
		})
	})
}

// openStoreForm creates or edits one ship-to destination. Pass nil to create.
func (a *App) openStoreForm(customer core.Customer, existing *core.CustomerStore) {
	code := newEntry("The client's own store number, e.g. 0047")
	name := newEntry("Required, e.g. Herald Square")

	line1 := newEntry("Street address")
	line2 := newEntry("Dock, suite or floor")
	city := newEntry("City")
	region := newEntry("State or region")
	postal := newEntry("Postal code")
	country := newEntry("Country")

	contactName := newEntry("Receiving contact")
	contactEmail := newEntry("name@example.com")
	contactPhone := newEntry("Phone")

	routing := widget.NewMultiLineEntry()
	routing.SetPlaceHolder("Delivery windows, appointment booking, carton labelling")
	routing.Wrapping = fyne.TextWrapWord

	active := widget.NewCheck("Store is open and can be shipped to", nil)
	active.SetChecked(true)

	if existing != nil {
		code.SetText(existing.Code)
		name.SetText(existing.Name)
		line1.SetText(existing.ShipTo.Line1)
		line2.SetText(existing.ShipTo.Line2)
		city.SetText(existing.ShipTo.City)
		region.SetText(existing.ShipTo.Region)
		postal.SetText(existing.ShipTo.PostalCode)
		country.SetText(existing.ShipTo.Country)
		contactName.SetText(existing.Contact.Name)
		contactEmail.SetText(existing.Contact.Email)
		contactPhone.SetText(existing.Contact.Phone)
		routing.SetText(existing.RoutingNotes)
		active.SetChecked(existing.Active)
	}

	identity := container.New(newFormGrid())
	formRow(identity, "Store code", code)
	formRow(identity, "Store name", name)

	address := container.New(newFormGrid())
	formRow(address, "Address line 1", line1)
	formRow(address, "Address line 2", line2)
	formRow(address, "City", city)
	formRow(address, "State or region", region)
	formRow(address, "Postal code", postal)
	formRow(address, "Country", country)

	contact := container.New(newFormGrid())
	formRow(contact, "Contact name", contactName)
	formRow(contact, "Email", contactEmail)
	formRow(contact, "Phone", contactPhone)

	routingNote := widget.NewLabel(
		"These appear on the delivery paperwork. Large retailers charge back for " +
			"missed appointments and wrong labels, so record what they ask for.")
	routingNote.Importance = widget.LowImportance
	routingNote.Wrapping = fyne.TextWrapWord

	body := container.NewVScroll(container.NewPadded(container.NewVBox(
		widget.NewLabelWithStyle(customer.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		identity,
		sectionHeading("Ship to"),
		address,
		sectionHeading("Receiving contact"),
		contact,
		sectionHeading("Routing requirements"),
		routingNote,
		routing,
		active,
	)))

	title, confirm := "New store for "+customer.Code, "Create"
	if existing != nil {
		title, confirm = "Edit store "+existing.Code, "Save changes"
	}

	submit := func(ok bool) {
		if !ok {
			return
		}

		store := core.CustomerStore{
			CustomerID: customer.ID,
			Code:       strings.TrimSpace(code.Text),
			Name:       strings.TrimSpace(name.Text),
			ShipTo: core.Address{
				Line1: line1.Text, Line2: line2.Text, City: city.Text,
				Region: region.Text, PostalCode: postal.Text, Country: country.Text,
			},
			Contact: core.Contact{
				Name: contactName.Text, Email: contactEmail.Text, Phone: contactPhone.Text,
			},
			RoutingNotes: routing.Text,
			Active:       active.Checked,
		}
		if existing != nil {
			store.ID = existing.ID
			store.Version = existing.Version
			store.CreatedAt = existing.CreatedAt
		}
		a.saveStore(store, existing == nil)
	}

	d := dialog.NewCustomConfirm(title, confirm, "Cancel", body, submit, a.window)
	d.Resize(fyne.NewSize(680, 660))
	d.Show()
	a.window.Canvas().Focus(code)
}

func (a *App) saveStore(store core.CustomerStore, creating bool) {
	a.background(func(ctx context.Context) {
		var err error
		if creating {
			store, err = a.svc.CreateStore(ctx, store)
		} else {
			store, err = a.svc.UpdateStore(ctx, store)
		}

		a.onMain(func() {
			if err != nil {
				a.showError(err)
				return
			}
			a.status.success("Saved store %s.", store.Label())
			a.reloadAll()
		})
	})
}
