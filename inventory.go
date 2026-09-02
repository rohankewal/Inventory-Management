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

// AddProduct now expects values to be passed directly from the GUI components
func AddProduct(db *sql.DB, name string, price float64, stock int) error {
	query := "INSERT INTO products (name, price, stock) VALUES (?, ?, ?)"
	_, err := db.Exec(query, name, price, stock)
	return err
}

// RemoveProduct takes the ID directly from the UI selection
func RemoveProduct(db *sql.DB, id int) (int64, error) {
	query := "DELETE FROM products WHERE id = ?"
	result, err := db.Exec(query, id)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
