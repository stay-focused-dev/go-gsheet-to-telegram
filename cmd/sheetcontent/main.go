package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

func main() {
	ctx := context.Background()

	credBytes, err := os.ReadFile("/tmp/credentials.json")
	if err != nil {
		log.Fatalf("Unable to read credentials: %v", err)
	}

	config, err := google.JWTConfigFromJSON(
		credBytes,
		drive.DriveReadonlyScope,
		sheets.SpreadsheetsReadonlyScope,
	)

	if err != nil {
		log.Fatalf("Unable to parse credentials: %v", err)
	}

	client := config.Client(ctx)
	sheetsService, err := sheets.NewService(ctx, option.WithHTTPClient((client)))
	if err != nil {
		log.Fatalf("Unable to create sheets service: %v", err)
	}

	sheetId := "1W0wgyCA2MIasx5zsFCKODJk2iSUKx3APpzoDs-9mWXE"
	readRange := "Лист1!A1:F10"

	resp, err := sheetsService.Spreadsheets.Values.Get(sheetId, readRange).Do()
	if err != nil {
		log.Fatalf("Unable to retrieve data: %v", err)
	}

	if len(resp.Values) == 0 {
		fmt.Println("No data found.")
	} else {
		for _, row := range resp.Values {
			fmt.Printf("%v\n", row)
		}
	}
}
