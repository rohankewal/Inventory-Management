package main

import (
	"fmt"
	"log"
)

func main() {
	db, err := InitDB("inventory.db")
	if err != nil {
		log.Fatalf("❌ Failed to connect to SQLite database: %v", err)
	}
	defer db.Close()

	for {
		fmt.Println("\n=========================")
		fmt.Println("   INVENTORY MANAGEMENT   ")
		fmt.Println("=========================")
		fmt.Println("1. View Catalog")
		fmt.Println("2. Add Product")
		fmt.Println("3. Remove Product")
		fmt.Println("4. Exit Application")
		fmt.Println("=========================")

		choice := readInt("Select an option (1-4): ")

		switch choice {
		case 1:
			DisplayCatalog(db)
		case 2:
			AddProduct(db)
		case 3:
			RemoveProduct(db)
		case 4:
			fmt.Println("\nExiting system. Goodbye!")
			return // Terminates main() and exits the app
		default:
			fmt.Println("❌ Invalid choice. Please choose a number between 1 and 4.")
		}
	}
}
