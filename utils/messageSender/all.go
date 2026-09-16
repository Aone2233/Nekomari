package messageSender

import (
	_ "github.com/Aone2233/nekomari/utils/messageSender/bark"
	_ "github.com/Aone2233/nekomari/utils/messageSender/email"
	_ "github.com/Aone2233/nekomari/utils/messageSender/empty"
	_ "github.com/Aone2233/nekomari/utils/messageSender/javascript"
	_ "github.com/Aone2233/nekomari/utils/messageSender/serverchan3"
	_ "github.com/Aone2233/nekomari/utils/messageSender/serverchanturbo"
	_ "github.com/Aone2233/nekomari/utils/messageSender/telegram"
	_ "github.com/Aone2233/nekomari/utils/messageSender/webhook"
)

func All() {
}
