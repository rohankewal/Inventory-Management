package main

import (
	"fmt"
	"log"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

func main() {
	// 1. Setup the Database Connection
	db, err := InitDB("inventory.db")
	if err != nil {
		log.Fatalf("Database connection failure: %v", err)
	}
	defer db.Close()

	// 2. Initialize the Fyne Application instance
	myApp := app.New()
	myWindow := myApp.NewWindow("Enterprise Inventory Management")
	myWindow.Resize(fyne.NewSize(700, 450))

	// 3. Define the UI Data Display components
	statusLabel := widget.NewLabel("System Ready.")
	catalogDisplay := widget.NewMultiLineEntry()
	catalogDisplay.Disable() // Read-only view for output tracking

	// Helper function to pull database rows and refresh our visual screen
	refreshUIList := func() {
		rows, err := db.Query("SELECT id, name, price, stock FROM products")
		if err != nil {
			statusLabel.SetText("❌ Failed to load inventory.")
			return
		}
		defer rows.Close()

		var updatedText string
		for rows.Next() {
			var p Product
			rows.Scan(&p.ID, &p.Name, &p.Price, &p.Stock)
			updatedText += fmt.Sprintf("ID: %04d | Name: %-20s | Price: $%6.2f | Stock: %d\n", p.ID, p.Name, p.Price, p.Stock)
		}
		catalogDisplay.SetText(updatedText)
	}

	// Load data instantly on boot
	refreshUIList()

	// 4. Create the Input Field Components
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("Product Name")

	priceEntry := widget.NewEntry()
	priceEntry.SetPlaceHolder("Price (e.g. 12.50)")

	stockEntry := widget.NewEntry()
	stockEntry.SetPlaceHolder("Initial Stock Count")

	// 5. Button Actions (Replacing the terminal logic)
	addButton := widget.NewButton("Add Product", func() {
		// Convert text field data types safely with GUI error catch blocks
		price, err1 := strconv.ParseFloat(priceEntry.Text, 64)
		stock, err2 := strconv.Atoi(stockEntry.Text)

		if nameEntry.Text == "" || err1 != nil || err2 != nil {
			statusLabel.SetText("❌ Error: Invalid fields. Please double check entries.")
			return
		}

		// Send values to our refactored inventory function
		err := AddProduct(db, nameEntry.Text, price, stock)
		if err != nil {
			statusLabel.SetText("❌ Error saving item to database.")
			return
		}

		// Success path: clear inputs, report, and reload catalog screen view
		statusLabel.SetText(fmt.Sprintf("✅ Added %s successfully!", nameEntry.Text))
		nameEntry.SetText("")
		priceEntry.SetText("")
		stockEntry.SetText("")
		refreshUIList()
	})

	removeIDEntry := widget.NewEntry()
	removeIDEntry.SetPlaceHolder("ID to Delete")

	removeButton := widget.NewButton("Delete Item", func() {
		id, err := strconv.Atoi(removeIDEntry.Text)
		if err != nil {
			statusLabel.SetText("❌ Please provide a valid numerical ID.")
			return
		}

		affected, err := RemoveProduct(db, id)
		if err != nil || affected == 0 {
			statusLabel.SetText("❌ Could not remove. ID might not exist.")
			return
		}

		statusLabel.SetText(fmt.Sprintf("✅ Product %d removed.", id))
		removeIDEntry.SetText("")
		refreshUIList()
	})

	// 6. Layout Architecture Structure Configuration
	inputForm := container.NewVBox(
		widget.NewLabel("🆕 REGISTER PRODUCT"),
		nameEntry,
		priceEntry,
		stockEntry,
		addButton,
		widget.NewSeparator(),
		widget.NewLabel("🗑️ INVENTORY REMOVAL"),
		removeIDEntry,
		removeButton,
	)

	// Combine components into a neat split window panel layout
	mainLayout := container.NewHSplit(
		inputForm,
		container.NewBorder(widget.NewLabel("📋 CURRENT STOCK LEDGER"), statusLabel, nil, nil, catalogDisplay),
	)
	mainLayout.SetOffset(0.35) // Dedicate 35% window canvas space leftward

	myWindow.SetContent(mainLayout)
	myWindow.ShowAndRun()
}
