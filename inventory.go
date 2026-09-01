package main

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Create the database
func InitDB(filepath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", filepath)
	if err != nil {
		return nil, err
	}

	// Create a table for products. We let SQLite manage the auto-incrementing ID.
	query := `
		CREATE TABLE IF NOT EXISTS products (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			price REAL NOT NULL,
			stock INTEGER NOT NULL
		);`

	_, err = db.Exec(query)
	if err != nil {
		return nil, err
	}

	return db, nil
}

// DisplayCatalog checks and prints the entire product list
func DisplayCatalog(db *sql.DB) {
	rows, err := db.Query("SELECT id, name, price, stock FROM products")
	if err != nil {
		fmt.Println("❌ Error reading database:", err)
		return
	}
	defer rows.Close() // Ensure connection resource is cleaned up when done

	hasItems := false
	fmt.Println("\n--- Current Product Catalog ---")

	// Iterate through the database cursor rows
	for rows.Next() {
		hasItems = true
		var p Product
		// Scan columns into struct fields
		err := rows.Scan(&p.ID, &p.Name, &p.Price, &p.Stock)
		if err != nil {
			fmt.Println("❌ Error scanning product row:", err)
			continue
		}
		fmt.Println(p)
	}

	if !hasItems {
		fmt.Println("📋 No products added yet - add one now!")
	}
}

// AddProduct inserts a brand new row into the database
func AddProduct(db *sql.DB) {
	fmt.Println("\n--- Add New Product ---")
	// Notice we no longer ask for ID, SQLite manages it automatically
	name := readString("Enter Product Name: ")
	price := readFloat("Enter Product Price: ")
	stock := readInt("Enter Product Stock: ")

	query := "INSERT INTO products (name, price, stock) VALUES (?, ?, ?)"
	_, err := db.Exec(query, name, price, stock)
	if err != nil {
		fmt.Println("❌ Error saving product:", err)
		return
	}

	fmt.Printf("✅ Product '%s' added and saved successfully.\n", name)
}

// RemoveProduct runs a DELETE statement targeting a specific record ID
func RemoveProduct(db *sql.DB) {
	fmt.Println("\n--- Remove Product ---")
	idToRemove := readInt("Enter the ID of the product to remove: ")

	query := "DELETE FROM products WHERE id = ?"
	result, err := db.Exec(query, idToRemove)
	if err != nil {
		fmt.Println("❌ Error running delete statement:", err)
		return
	}

	// Check if a row was actually impacted by the delete query
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		fmt.Printf("❌ Error: Product with ID %d not found.\n", idToRemove)
		return
	}

	fmt.Printf("✅ Product %d successfully removed from storage.\n", idToRemove)
}
