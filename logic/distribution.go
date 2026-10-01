package logic

import (
	"math/rand"
	"time"

	"github.com/Hund06/BotVariant/db"
)

// Distribute выдает варианты по принципу перемешанной колоды
func Distribute(teams []db.Team, variantsCount int) {
	if len(teams) == 0 || variantsCount <= 0 {
		return
	}

	rand.Seed(time.Now().UnixNano())

	// Функция для создания новой перемешанной колоды со всеми вариантами
	generateDeck := func() []int {
		deck := make([]int, variantsCount)
		for i := 0; i < variantsCount; i++ {
			deck[i] = i + 1
		}
		// Тщательно тасуем колоду
		rand.Shuffle(len(deck), func(i, j int) {
			deck[i], deck[j] = deck[j], deck[i]
		})
		return deck
	}

	// Берем первую колоду
	currentDeck := generateDeck()
	deckIndex := 0

	// Раздаем варианты командам
	for i := range teams {
		// Если карты в колоде закончились, достаем новую тасованную колоду
		if deckIndex >= len(currentDeck) {
			currentDeck = generateDeck()
			deckIndex = 0
		}

		teams[i].Variant = currentDeck[deckIndex]
		deckIndex++
	}
}
