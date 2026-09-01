package main

import "fmt"

// DisplayCatalog checks and prints the entire product list
func DisplayCatalog(catalog []Product) {
	if len(catalog) == 0 {
		fmt.Println("\n📋 No products added yet - add one now!")
		return
	}
	fmt.Println("\n--- Current Product Catalog ---")
	for _, item := range catalog {
		fmt.Println(item)
	}
}

// AddProduct handles user inputs and appends the new item to the slice
func AddProduct(catalog []Product) []Product {
	fmt.Println("\n--- Add New Product ---")
	id := readInt("Enter Product ID: ")
	name := readString("Enter Product Name: ")
	price := readFloat("Enter Product Price: ")
	stock := readInt("Enter Product Stock: ")

	newProd := Product{ID: id, Name: name, Price: price, Stock: stock}
	catalog = append(catalog, newProd)

	fmt.Printf("✅ Product '%s' added successfully.\n", name)
	return catalog
}

// RemoveProduct locates a product by ID and cuts it out of the slice
func RemoveProduct(catalog []Product) []Product {
	if len(catalog) == 0 {
		fmt.Println("\n❌ The inventory is currently empty.")
		return catalog
	}

	fmt.Println("\n--- Remove Product ---")
	idToRemove := readInt("Enter the ID of the product to remove: ")

	targetIndex := -1
	for idx, item := range catalog {
		if item.ID == idToRemove {
			targetIndex = idx
			break
		}
	}

	if targetIndex == -1 {
		fmt.Printf("❌ Error: Product with ID %d not found.\n", idToRemove)
		return catalog
	}

	catalog = append(catalog[:targetIndex], catalog[targetIndex+1:]...)
	fmt.Printf("✅ Product %d successfully removed.\n", idToRemove)
	return catalog
}
