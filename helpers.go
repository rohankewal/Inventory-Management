package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

var reader = bufio.NewReader(os.Stdin)

// check for a valid int and handle errors as well
func readInt(prompt string) int {
	for {
		var val int
		fmt.Print(prompt)
		_, err := fmt.Scanf("%d\n", &val)
		if err != nil {
			fmt.Println("❌ Error: Invalid format. Please enter a whole number.")
			reader.ReadString('\n') // Clear broken text from input buffer
			continue
		}
		return val
	}
}

// check for a valid float and handle errors as well
func readFloat(prompt string) float64 {
	for {
		var val float64
		fmt.Print(prompt)
		_, err := fmt.Scanf("%f\n", &val)
		if err != nil {
			fmt.Println("❌ Error: Invalid format. Please enter a valid decimal number.")
			reader.ReadString('\n') // Clear broken text from input buffer
			continue
		}
		return val
	}
}

// check for a valid string and handle errors as well
func readString(prompt string) string {
	for {
		fmt.Print(prompt)
		input, err := reader.ReadString('\n')
		cleaned := strings.TrimSpace(input)
		if err != nil || cleaned == "" {
			fmt.Println("❌ Error: This field cannot be left blank.")
			continue
		}
		return cleaned
	}
}
