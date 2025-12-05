package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func usage() {
	program := filepath.Base(os.Args[0])
	fmt.Printf("Usage: %s -token TGTOKEN", program)
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  -token string")
	fmt.Println("        Telegram token for a bot (required)")
	fmt.Println("Example:")
	fmt.Printf("  %s -token SOMESECRETTOKEN -chat SOMECHATID\n", program)
	os.Exit(1)
}
func main() {
	tgtoken := flag.String("token", "", "Token for a telegram bot")

	flag.Parse()

	if *tgtoken == "" {
		usage()
	}

	bot, err := tgbotapi.NewBotAPI(*tgtoken)
	if err != nil {
		log.Fatal(err)
	}

	bot.Debug = true

	log.Printf("Authorized on account %s", bot.Self.UserName)

	var chatId atomic.Int64
	go sendNotifications(bot, &chatId)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil && update.Message.Text != "" {
			chatId.Store(update.Message.Chat.ID)
			go handleMessage(bot, update.Message)
		}
	}
}

func handleMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	username := "Unknown"
	if message.From != nil {
		username = message.From.UserName
		if username == "" {
			username = message.From.FirstName
		}
	}
	log.Printf("[%s] %s", username, message.Text)

	msg := tgbotapi.NewMessage(message.Chat.ID, message.Text)
	msg.ReplyToMessageID = message.MessageID

	if _, err := bot.Send(msg); err != nil {
		log.Printf("Error sending reply: %v", err)
	}
}

func sendNotifications(bot *tgbotapi.BotAPI, chatId *atomic.Int64) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		id := chatId.Load()
		if id != 0 {
			msg := tgbotapi.NewMessage(id, "Notification: "+time.Now().Format(time.RFC3339))
			if _, err := bot.Send(msg); err != nil {
				log.Printf("Error sending notification: %v", err)
			} else {
				log.Println("Sent scheduled notification")
			}
		}
	}
}
