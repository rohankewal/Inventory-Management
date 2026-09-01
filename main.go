package main

import "fmt"

func main() {
	var catalog []Product

	// Seed data so it doesn't start empty
	catalog = append(catalog, Product{ID: 1, Name: "Coffee Mug", Price: 12.50, Stock: 100})

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
			DisplayCatalog(catalog)
		case 2:
			catalog = AddProduct(catalog)
		case 3:
			catalog = RemoveProduct(catalog)
		case 4:
			fmt.Println("\nExiting system. Goodbye!")
			return // Terminates main() and exits the app
		default:
			fmt.Println("❌ Invalid choice. Please choose a number between 1 and 4.")
		}
	}
}
