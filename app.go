package main

import (
        "context"
        "fmt"
        "log"
        "strings"
        "sync"

        tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"

        "github.com/google/generative-ai-go/genai"
        "github.com/spf13/viper"
        "google.golang.org/api/option"
)

type Config struct {
        TelegramToken string `mapstructure:"tgToken"`
        GptToken      string `mapstructure:"gptToken"`
        Preamble      string `mapstructure:"preamble"`
}

type Job struct {
        msgId   int
        chatId  int64
        reqText string
        APIKEY  string
}

type Result struct {
        msgId   int
        chatId  int64
        resText string
}

func LoadConfig(path string) (c Config, err error) {
        viper.SetConfigName("config")
        viper.SetConfigType("yaml")
        viper.AddConfigPath(path)

        viper.AutomaticEnv()

        err = viper.ReadInConfig()

        if err != nil {
                return
        }

        err = viper.Unmarshal(&c)
        return
}

func sendGemini(apiKey, sendText string) string {
        ctx := context.Background()
        client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
        if err != nil {
                log.Fatal(err)
        }
        defer client.Close()

        model := client.GenerativeModel("gemini-1.5-flash")
        resp, err := model.GenerateContent(ctx, genai.Text(sendText))
        if err != nil {
                log.Fatal(err)
        }

        // Iterate through the response and capture the content you want
        var result string
        for _, cand := range resp.Candidates {
                if cand.Content != nil {
                        for _, part := range cand.Content.Parts {
                                // Type assertion to ensure part is a string
                                if res, ok := part.(genai.Text); ok {
                                        result = string(res) // Correctly extract the text content
                                }
                        }
                }
        }

        return result // Return the captured content
}

func worker(processorID int, jobs <-chan Job, results chan<- Result, wg *sync.WaitGroup) {
        defer wg.Done()
        for job := range jobs {
                newText := sendGemini(job.APIKEY, job.reqText)
                newText += fmt.Sprintf("\n\nProcessed by goroutine %d", processorID)
                results <- Result{msgId: job.msgId, chatId: job.chatId, resText: newText}
        }

}

func dispatch(bot *tgbotapi.BotAPI, senderID int, results <-chan Result, wg *sync.WaitGroup) {
        defer wg.Done()
        for result := range results {
                output := result.resText
                output += fmt.Sprintf("\n\n sent by goroutine  %d", senderID)
                msg := tgbotapi.NewMessage(result.chatId, output)
                msg.ReplyToMessageID = result.msgId

                _, err := bot.Send(msg)
                if err != nil {
                        log.Println("Error:", err)
                }

        }

}

func main() {
        var userPrompt string
        var gptPrompt string
        // Reading config.yaml
        config, err := LoadConfig(".")
        apiKey := config.GptToken

        if err != nil {
                panic(fmt.Errorf("fatal error with config.yaml: %w", err))
        }

        bot, err := tgbotapi.NewBotAPI(config.TelegramToken)
        if err != nil {
                log.Panic(err)
        }

        bot.Debug = true // set to false for suppress logs in stdout
        log.Printf("Authorized on account %s", bot.Self.UserName)

        // Start Telegram long polling update
        u := tgbotapi.NewUpdate(0)
        u.Timeout = 60
        updates, err := bot.GetUpdatesChan(u)
        if err != nil {
                log.Println("Error:", err)
        }

        jobs := make(chan Job)
        results := make(chan Result)

        workerCount := 10
        var wg1 sync.WaitGroup
        wg1.Add(workerCount)

        for i := 1; i <= workerCount; i++ {
                go worker(i, jobs, results, &wg1)
        }

        dispatchercount := 10
        var wg2 sync.WaitGroup
        wg2.Add(dispatchercount)
        for i := 1; i <= dispatchercount; i++ {
                go dispatch(bot, i, results, &wg2)
        }

        for update := range updates {
                if update.Message == nil {
                        continue
                }

                // check if message has '/topic' or '/phrase' as prefix
                if !strings.HasPrefix(update.Message.Text, "/topic") && !strings.HasPrefix(update.Message.Text, "/phrase") {
                        continue
                }

                // remove '/topic' or '/phrase' from message
                if strings.HasPrefix(update.Message.Text, "/topic") {
                        userPrompt = strings.TrimPrefix(update.Message.Text, "/topic")
                        gptPrompt = config.Preamble + "TOPIC: "
                } else if strings.HasPrefix(update.Message.Text, "/phrase") {
                        userPrompt = strings.TrimPrefix(update.Message.Text, "/phrase")
                        gptPrompt = config.Preamble + "PHRASE: "
                }

                if userPrompt != "" {
                        gptPrompt += userPrompt

                        jobs <- Job{
                                msgId:   update.Message.MessageID,
                                chatId:  update.Message.Chat.ID,
                                reqText: gptPrompt,
                                APIKEY:  apiKey,
                        }

                } else {
                        update.Message.Text = "Please, enter your topic or phrase"
                }

                // Send message to Telegram

        }
        close(jobs)
        wg1.Wait()
        close(results)
        wg2.Wait()

}
