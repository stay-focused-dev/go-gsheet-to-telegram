package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

const MaxChannelDuration = 24 * time.Hour

type DriveWatcher struct {
	service        *drive.Service
	webhookURL     string
	activeChannels map[string]*ChannelInfo
}

type ChannelInfo struct {
	Id         string
	ResourceId string
	Expiration time.Time
}

func NewDriveWatcher(credentials []byte, webhookURL string) (*DriveWatcher, error) {
	ctx := context.Background()

	config, err := google.JWTConfigFromJSON(credentials, drive.DriveScope)
	if err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %w", err)
	}

	client := config.Client(ctx)

	service, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("failed to create drive service: %w", err)
	}

	return &DriveWatcher{
		service:        service,
		webhookURL:     webhookURL,
		activeChannels: make(map[string]*ChannelInfo),
	}, nil
}

func (dw *DriveWatcher) WatchFile(fileId string) (*ChannelInfo, error) {
	channelId := uuid.New().String()
	expiration := time.Now().Add(MaxChannelDuration).UnixMilli()

	channel := &drive.Channel{
		Id:         channelId,
		Type:       "web_hook",
		Address:    dw.webhookURL,
		Expiration: expiration,
	}

	log.Printf("Creatiing watch for file %s with channel %s", fileId, channelId)

	result, err := dw.service.Files.Watch(fileId, channel).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to watch file: %w", err)
	}

	info := &ChannelInfo{
		Id:         result.Id,
		ResourceId: result.ResourceId,
		Expiration: time.UnixMilli(result.Expiration),
	}

	dw.activeChannels[result.Id] = info
	log.Printf("Watch created: Channel=%s, Resource=%s, Expires=%s", info.Id, info.ResourceId, info.Expiration)
	return info, nil
}

func (dw *DriveWatcher) StopWatch(channelId string) error {
	info, exists := dw.activeChannels[channelId]
	if !exists {
		return fmt.Errorf("channel %s not found", channelId)
	}

	stopChannel := &drive.Channel{
		Id:         info.Id,
		ResourceId: info.ResourceId,
	}

	err := dw.service.Channels.Stop(stopChannel).Do()
	if err != nil {
		return fmt.Errorf("failed to stop channel: %w", err)
	}

	delete(dw.activeChannels, channelId)
	log.Printf("Channel %s stopped", channelId)

	return nil
}

func (dw *DriveWatcher) RenewWatch(fileId, oldChannelId string) (*ChannelInfo, error) {
	if err := dw.StopWatch(oldChannelId); err != nil {
		log.Printf("Warning: failed to stop old channel: %v", err)
	}

	return dw.WatchFile(fileId)
}

func (dw *DriveWatcher) StartChannelRenewer(fileId string) {
	ticker := time.NewTicker(20 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		for channelId, info := range dw.activeChannels {
			if time.Until(info.Expiration) < 4*time.Hour {
				log.Printf("Renewing channel (%s) (expires soon)", channelId)

				newInfo, err := dw.RenewWatch(fileId, channelId)
				if err != nil {
					log.Printf("Failed to renew channel %s: %v", channelId, err)
				} else {
					log.Printf("Channel renewed: %s -> %s", channelId, newInfo.Id)
				}
			}
		}
	}
}

type DriveNotification struct {
	ChannelId     string `header:"X-Goog-Channel-ID"`
	ResourceId    string `header:"X-Goog-Resource-ID"`
	ResourceState string `header:"X-Goog-Resource-State"`
	ResourceURI   string `header:"X-Goog-Resource-URI"`
	MessageNumber string `header:"X-Goog-Message-Number"`
	Expiration    string `header:"X-Goog-Channel-Expiration"`
}

func (dw *DriveWatcher) WebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	notification := DriveNotification{
		ChannelId:     r.Header.Get("X-Goog-Channel-ID"),
		ResourceId:    r.Header.Get("X-Goog-Resource-ID"),
		ResourceState: r.Header.Get("X-Goog-Resource-State"),
		ResourceURI:   r.Header.Get("X-Goog-Resource-URI"),
		MessageNumber: r.Header.Get("X-Goog-Message-Number"),
		Expiration:    r.Header.Get("X-Goog-Channel-Expiration"),
	}

	log.Printf("Received notification: Channel=%s, State=%s, Resource=%s, Msg#=%s",
		notification.ChannelId,
		notification.ResourceState,
		notification.ResourceId,
		notification.MessageNumber,
	)

	switch notification.ResourceState {
	case "sync":
		log.Printf("Channel %s syncronized", notification.ChannelId)
	case "change":
		log.Printf("File changed! Channel=%s, Resource=%s",
			notification.ChannelId, notification.ResourceId,
		)
		go dw.handleFileChange(notification)
	case "update":
		log.Printf("File metadata updated: %s", notification.ResourceId)
	default:
		log.Printf("Unknown state: %s", notification.ResourceState)
	}

	w.WriteHeader(http.StatusOK)

}

func (dw *DriveWatcher) handleFileChange(notification DriveNotification) {
	log.Printf("Processing file change for resource %s", notification.ResourceId)
}

func usage() {
	program := filepath.Base(os.Args[0])
	fmt.Printf("Usage: ./%s -creds CREDS_FILE -webhook WEBHOOK_URL -sheet GSHEET_ID -port WEBHOOK_PORT", program)
	os.Exit(1)
}
func main() {
	credentialsFile := flag.String("creds", "", "file with JSON credentials to GDrive API")
	webhookURL := flag.String("webhook", "", "webhook URL for GDrive")
	sheetId := flag.String("sheet", "", "sheet id")
	port := flag.Int("port", 8080, "port for webhook URL")

	flag.Parse()

	if *credentialsFile == "" || *webhookURL == "" || *sheetId == "" {
		usage()
	}

	credentials, err := os.ReadFile(*credentialsFile)
	if err != nil {
		log.Fatalf("Unable to read credentials file: %v", err)
	}

	watcher, err := NewDriveWatcher(credentials, *webhookURL)
	if err != nil {
		log.Fatal(err)
	}

	_, err = watcher.WatchFile(*sheetId)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Watching sheet: %s", *sheetId)

	go watcher.StartChannelRenewer(*sheetId)

	http.HandleFunc("/drive-webhook", watcher.WebhookHandler)

	go func() {
		hostport := fmt.Sprintf(":%d", *port)
		log.Printf("Starting webhook server on %s\n", hostport)
		if err := http.ListenAndServe(hostport, nil); err != nil {
			log.Fatal(err)
		}
	}()

	select {}
}
