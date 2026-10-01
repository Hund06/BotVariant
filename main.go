package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Hund06/BotVariant/db"
	"github.com/Hund06/BotVariant/logic"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

// main точка входа в программу
func main() {
	godotenv.Load()

	botToken := os.Getenv("BOT_TOKEN")
	if botToken == "" {
		log.Panic("Токен бота пуст. Проверьте .env")
	}

	proxyStr := os.Getenv("PROXY_URL")
	var client *http.Client

	if proxyStr != "" {
		proxyURL, err := url.Parse(proxyStr)
		if err != nil {
			log.Panic("Ошибка парсинга PROXY_URL: ", err)
		}
		transport := &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		}
		client = &http.Client{Transport: transport}
	} else {
		client = &http.Client{}
	}

	bot, err := tgbotapi.NewBotAPIWithClient(botToken, tgbotapi.APIEndpoint, client)
	if err != nil {
		log.Panic(err)
	}

	// Инициализируем базу данных в самом начале
	database, err := db.InitDB("bot_database.db")
	if err != nil {
		log.Panic(err)
	}

	commandsList := []tgbotapi.BotCommand{
		{Command: "add_team", Description: "Добавить команду (отправить ФИО одним сообщением)"},
		{Command: "remind", Description: "Напомнить выданные варианты"},
		{Command: "clear", Description: "Очистить список команд"},
		{Command: "cancel", Description: "Отменить текущее действие ввода"},
	}

	allowManual, _ := strconv.ParseBool(os.Getenv("ALLOW_MANUAL_DISTRIBUTE"))
	if allowManual {
		commandsList = append(commandsList, tgbotapi.BotCommand{
			Command: "distribute", Description: "Распределить варианты прямо сейчас",
		})
	}

	bot.Request(tgbotapi.NewSetMyCommands(commandsList...))

	// Запускаем фоновый планировщик (теперь database уже определена)
	go startScheduler(bot, database)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil {
			handleMessage(bot, database, update.Message)
		}
	}
}

// handleMessage обрабатывает команды и текст с умным парсингом упоминаний
func handleMessage(bot *tgbotapi.BotAPI, database *db.Database, message *tgbotapi.Message) {
	chatID := message.Chat.ID
	text := strings.TrimSpace(message.Text)

	if text == "" {
		return
	}

	// Умный парсинг команд (поддержка формата @BotName /команда)
	botNamePrefix := "@" + bot.Self.UserName
	isCommand := false
	var command string

	if strings.HasPrefix(text, botNamePrefix) {
		text = strings.TrimSpace(strings.TrimPrefix(text, botNamePrefix))
		if !strings.HasPrefix(text, "/") && text != "" {
			text = "/" + text
		}
	}

	if strings.HasPrefix(text, "/") {
		isCommand = true
		parts := strings.Fields(text)
		command = parts[0][1:]
		if idx := strings.Index(command, "@"); idx != -1 {
			command = command[:idx]
		}
	}

	if isCommand {
		switch command {
		case "add_team":
			database.SetChatState(chatID, "WAITING_MEMBERS")
			bot.Send(tgbotapi.NewMessage(chatID, "Жду ввода! Отправьте участников команды одним сообщением:"))

		case "cancel":
			database.SetChatState(chatID, "IDLE")
			bot.Send(tgbotapi.NewMessage(chatID, "Действие отменено. Бот переведен в режим ожидания."))

		case "remind":
			bot.Send(tgbotapi.NewMessage(chatID, "Собираю актуальные данные о вариантах..."))
			sendDistributionMessage(bot, database, chatID)

		case "distribute":
			allowManual, _ := strconv.ParseBool(os.Getenv("ALLOW_MANUAL_DISTRIBUTE"))
			if allowManual {
				bot.Send(tgbotapi.NewMessage(chatID, "Начинаю распределение вариантов..."))
				variantsCount, _ := strconv.Atoi(os.Getenv("VARIANTS_COUNT"))
				distributeAndSave(database, chatID, variantsCount)
				sendDistributionMessage(bot, database, chatID)
			} else {
				bot.Send(tgbotapi.NewMessage(chatID, "Команда ручного распределения отключена в настройках."))
			}

		case "clear":
			database.ClearTeams(chatID)
			database.SetChatState(chatID, "IDLE")
			bot.Send(tgbotapi.NewMessage(chatID, "Списки команд очищены. База пуста."))
		}
		return
	}

	// Обработка текста, когда бот ждет состав участников
	state, _ := database.GetChatState(chatID)

	if state == "WAITING_MEMBERS" {
		members := parseMembers(text)

		if len(members) == 0 {
			bot.Send(tgbotapi.NewMessage(chatID, "Не удалось распознать участников. Попробуйте еще раз или напишите /cancel"))
			return
		}

		err := database.CreateTeamWithMembers(chatID, members)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, "Произошла системная ошибка при сохранении команды."))
			return
		}

		database.SetChatState(chatID, "IDLE")
		reply := fmt.Sprintf("Команда успешно сохранена!\nСостав:\n- %s", strings.Join(members, "\n- "))
		bot.Send(tgbotapi.NewMessage(chatID, reply))
	}
}

// parseMembers разбивает текст на массив ФИО
func parseMembers(text string) []string {
	var result []string
	text = strings.ReplaceAll(text, "\n", ",")
	parts := strings.Split(text, ",")

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// startScheduler проверяет время каждую минуту и запускает рассылку
func startScheduler(bot *tgbotapi.BotAPI, database *db.Database) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		log.Println("Не удалось загрузить часовой пояс Москвы. Берем системный.")
		loc = time.Local
	}

	variantsCount, _ := strconv.Atoi(os.Getenv("VARIANTS_COUNT"))
	targetDay := parseWeekday(os.Getenv("DISTRIBUTE_DAY"))
	targetTime := os.Getenv("DISTRIBUTE_TIME")

	var lastRun time.Time

	for {
		now := time.Now().In(loc)

		if now.Weekday() == targetDay && now.Format("15:04") == targetTime {
			if now.Sub(lastRun) > time.Minute {
				lastRun = now

				chats, _ := database.GetAllChats()
				for _, chatID := range chats {
					bot.Send(tgbotapi.NewMessage(chatID, "Сработал таймер расписания. Начинаю распределение вариантов..."))
					distributeAndSave(database, chatID, variantsCount)
					sendDistributionMessage(bot, database, chatID)
				}
			}
		}
		time.Sleep(30 * time.Second)
	}
}

// distributeAndSave выдает варианты и сохраняет их в базу данных
func distributeAndSave(database *db.Database, chatID int64, variantsCount int) {
	teams, _ := database.GetAllTeams(chatID)
	if len(teams) == 0 {
		return
	}

	logic.Distribute(teams, variantsCount)
	for _, t := range teams {
		database.SaveTeamVariant(t.ID, t.Variant)
	}
}

// sendDistributionMessage собирает и отправляет сообщение с выданными вариантами
func sendDistributionMessage(bot *tgbotapi.BotAPI, database *db.Database, chatID int64) {
	teams, _ := database.GetAllTeams(chatID)

	if len(teams) == 0 {
		bot.Send(tgbotapi.NewMessage(chatID, "Список команд пуст. Добавьте команды через команду /add_team"))
		return
	}

	if teams[0].Variant == 0 {
		bot.Send(tgbotapi.NewMessage(chatID, "Варианты еще не распределены. Ожидайте автоматической выдачи по расписанию."))
		return
	}

	var sb strings.Builder
	sb.WriteString("РАСПРЕДЕЛЕНИЕ ВАРИАНТОВ:\n\n")
	for i, t := range teams {
		membersStr := strings.Join(t.Members, ", ")
		sb.WriteString(fmt.Sprintf("Команда №%d %s - Вариант %d\n", i+1, membersStr, t.Variant))
	}

	bot.Send(tgbotapi.NewMessage(chatID, sb.String()))
}

// parseWeekday переводит русское название дня в тип time.Weekday
func parseWeekday(day string) time.Weekday {
	day = strings.ToLower(strings.TrimSpace(day))
	switch day {
	case "понедельник":
		return time.Monday
	case "вторник":
		return time.Tuesday
	case "среда":
		return time.Wednesday
	case "четверг":
		return time.Thursday
	case "пятница":
		return time.Friday
	case "суббота":
		return time.Saturday
	case "воскресенье":
		return time.Sunday
	}
	return time.Wednesday
}
