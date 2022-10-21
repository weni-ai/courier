package telegram

import (
	"fmt"
	"strings"

	"github.com/nyaruka/courier/utils"
)

// KeyboardButton is button on a keyboard, see https://core.telegram.org/bots/api/#keyboardbutton
type KeyboardButton struct {
	Text            string `json:"text"`
	RequestContact  bool   `json:"request_contact,omitempty"`
	RequestLocation bool   `json:"request_location,omitempty"`
}

// ReplyKeyboardMarkup models a keyboard, see https://core.telegram.org/bots/api/#replykeyboardmarkup
type ReplyKeyboardMarkup struct {
	Keyboard        [][]KeyboardButton `json:"keyboard"`
	ResizeKeyboard  bool               `json:"resize_keyboard"`
	OneTimeKeyboard bool               `json:"one_time_keyboard"`
}

// NewKeyboardFromReplies creates a keyboard from the given quick replies
func NewKeyboardFromReplies(replies []string) *ReplyKeyboardMarkup {
	rows := utils.StringsToRows(replies, 5, 30, 2)
	keyboard := make([][]KeyboardButton, len(rows))

	for i := range rows {
		keyboard[i] = make([]KeyboardButton, len(rows[i]))
		for j := range rows[i] {
			fmt.Println(rows[i][j])
			var text string
			if strings.Contains(rows[i][j], "\\/") {
				text = strings.Replace(rows[i][j], "\\", "", -1)
			} else if strings.Contains(rows[i][j], "\\\\") {
				text = strings.Replace(rows[i][j], "\\\\", "\\", -1)
			} else {
				text = rows[i][j]
			}
			fmt.Println("New: ", text)
			keyboard[i][j].Text = text
		}
	}

	return &ReplyKeyboardMarkup{Keyboard: keyboard, ResizeKeyboard: true, OneTimeKeyboard: true}
}
