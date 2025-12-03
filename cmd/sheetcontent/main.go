package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

func usage() {
	program := filepath.Base(os.Args[0])
	fmt.Printf("Usage: ./%s -creds CREDS_FILE -sheet GSHEET_ID", program)
	os.Exit(1)
}

func main() {
	ctx := context.Background()

	credentialsFile := flag.String("creds", "", "file with JSON credentials to GDrive API")
	sheetId := flag.String("sheet", "", "sheet id")

	flag.Parse()

	if *credentialsFile == "" || *sheetId == "" {
		usage()
	}

	credBytes, err := os.ReadFile(*credentialsFile)
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

	readRange := "Лист1!A1:F10"

	resp, err := sheetsService.Spreadsheets.Values.Get(*sheetId, readRange).Do()
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
