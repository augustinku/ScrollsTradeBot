package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"
)

type State struct {
	con              net.Conn
	chRequests       chan Request
	chQuit           chan bool
	chMessages       chan Message
	chAddListener    chan Listener
	chRemoveListener chan Listener
	chTradeStatus    chan TradeStatus
	chTradeResponse  chan bool
}

type Listener chan Message
type Message struct {
	Text    string
	From    Player
	Channel Channel
}

var (
	CardTypes    = make(map[CardId]Card)
	CardRarities = make(map[Card]int)
	Libraries    = make(map[Player]MLibraryView)
	Stocks       = make(map[Player]map[Card]int)
	PlayerIds    = make(map[Player]string)
)

func InitState(con net.Conn) *State {
	s := State{con: con}
	s.chRequests = make(chan Request, 10)
	s.chQuit = make(chan bool, 5)
	s.chMessages = make(chan Message, 1)
	s.chAddListener = make(chan Listener, 1)
	s.chRemoveListener = make(chan Listener, 1)
	s.chTradeStatus = make(chan TradeStatus, 1)
	s.chTradeResponse = make(chan bool, 1)
	s.SendRequest(Request{"msg": "JoinLobby"})

	go func() {
		recv := make([]Listener, 0)

		for {
			select {
			case <-s.chQuit:
				s.chQuit <- true
				return
			case l := <-s.chAddListener:
				recv = append(recv, l)
			case l := <-s.chRemoveListener:
				for i, listener := range recv {
					if listener == l {
						recv[i], recv = recv[len(recv)-1], recv[:len(recv)-1]
					}
				}
			case m := <-s.chMessages:
				for _, l := range recv {
					l <- m
				}
			}
		}
	}()

	return &s
}

func (s *State) SendRequest(req Request) {
	log.Printf("-> %s\n", req)
	s.chRequests <- req
}

func (s *State) Listen() Listener {
	l := make(Listener, 1)
	s.chAddListener <- l
	return l
}

func (s *State) Shut(l Listener) {
	s.chRemoveListener <- l
}

func (s *State) JoinRoom(room Channel) {
	s.SendRequest(Request{"msg": "RoomEnter", "roomName": room})
	timeout := time.After(5 * time.Second)

	l := s.Listen()
	defer s.Shut(l)

	for {
		select {
		case <-timeout:
			return
		case m := <-l:
			if m.Channel == room {
				return
			}
		}
	}
}

func (s *State) LeaveRoom(room Channel) {
	s.SendRequest(Request{"msg": "RoomExit", "roomName": room})
}

func (s *State) Say(room Channel, text string) {
	s.SendRequest(Request{"msg": "RoomChatMessage", "text": text, "roomName": room})
}

func (s *State) Whisper(player Player, text string) {
	s.SendRequest(Request{"msg": "Whisper", "text": text, "toProfileName": player})
}

func (s *State) HandleReply(reply []byte) bool {
	if len(reply) < 2 {
		log.Printf("Error: reply is too short %s\n", reply)
		return false
	}

	var m Reply
	err := json.Unmarshal(reply, &m)
	if err != nil {
		log.Printf("Error: could not unmarshal reply %s\n", err)
		return false
	}

	if m.Msg != "AvatarTypes" &&
		m.Msg != "CardTypes" &&
		m.Msg != "AchievementTypes" &&
		m.Msg != "LibraryView" {
		log.Printf("<- %s", reply)
	}

	switch m.Msg {
	case "AchievementUnlocked":
		var v MAchievementUnlocked
		json.Unmarshal(reply, &v)

	case "AchievementTypes":
		var v MAchievementTypes
		json.Unmarshal(reply, &v)

	case "ActiveGame":
		var v MActiveGame
		json.Unmarshal(reply, &v)

	case "AvatarTypes":
		var v MAvatarTypes
		json.Unmarshal(reply, &v)

	case "CardTypes":
		var v MCardTypes
		json.Unmarshal(reply, &v)
		for _, cardType := range v.CardTypes {
			CardTypes[cardType.Id] = cardType.Name
			CardRarities[cardType.Name] = cardType.Rarity
		}
		LoadPrices()

	case "Fail":
		var v MFail
		json.Unmarshal(reply, &v)
		if v.Op == "TradeInvite" {
			s.chTradeResponse <- false
		}

	case "FatalFail":
		var v MFatalFail
		json.Unmarshal(reply, &v)
		fmt.Println(v)
		return false

	case "FriendRequestUpdate":
		var v MFriendRequestUpdate
		json.Unmarshal(reply, &v)
		s.SendRequest(Request{"msg": "AcceptFriendRequest", "requestId": v.Request.Request.Id})
		PlayerIds[v.Request.From.Profile.Name] = v.Request.From.Profile.Id

	case "FriendUpdate":
		var v MFriendUpdate
		json.Unmarshal(reply, &v)

	case "GetBlockedPersons":
		var v MGetBlockedPersons
		json.Unmarshal(reply, &v)

	case "GetFriendRequests":
		var v MGetFriendRequests
		json.Unmarshal(reply, &v)

		for _, request := range v.Requests {
			s.SendRequest(Request{"msg": "AcceptFriendRequest", "requestId": request.Request.Id})
			PlayerIds[request.From.Profile.Name] = request.From.Profile.Id
		}

	case "GetFriends":
		var v MGetFriends
		json.Unmarshal(reply, &v)

		for _, friend := range v.Friends {
			PlayerIds[friend.Profile.Name] = friend.Profile.Id
		}

	case "LibraryView":
		var v MLibraryView
		json.Unmarshal(reply, &v)

		var player Player
		for playerName, id := range PlayerIds {
			if id == v.ProfileId {
				player = playerName
				break
			}
		}

		Libraries[player] = v
		stock := make(map[Card]int)
		for _, card := range CardTypes {
			stock[card] = 0
		}

		for _, card := range v.Cards {
			if card.Tradable {
				name := CardTypes[card.TypeId]
				stock[name]++
			}
		}
		Stocks[player] = stock

	case "Ok":
		var v MOk
		json.Unmarshal(reply, &v)

	case "Ping":
		var v MPing
		json.Unmarshal(reply, &v)

	case "ProfileDataInfo":
		var v MProfileDataInfo
		json.Unmarshal(reply, &v)
		Gold = v.ProfileData.Gold

	case "ProfileInfo":
		var v MProfileInfo
		json.Unmarshal(reply, &v)
		Bot = v.Profile.Name
		s.SendRequest(Request{"msg": "LibraryView"})

	case "RoomChatMessage":
		var v MRoomChatMessage
		json.Unmarshal(reply, &v)
		s.chMessages <- Message{v.Text, v.From, Channel(v.RoomName)}

	case "RoomEnter":
		var v MRoomEnter
		json.Unmarshal(reply, &v)

	case "RoomInfo":
		var v MRoomInfo
		json.Unmarshal(reply, &v)
		for _, player := range v.Updated {
			PlayerIds[player.Name] = player.Id
		}
	case "ServerInfo":
		var v MServerInfo
		json.Unmarshal(reply, &v)

	case "TradeResponse":
		var v MTradeResponse
		json.Unmarshal(reply, &v)
		s.ParseTradeResponse(v)

	case "TradeView":
		var v MTradeView
		json.Unmarshal(reply, &v)
		s.ParseTradeView(v)

	case "Whisper":
		var v MWhisper
		json.Unmarshal(reply, &v)
		s.chMessages <- Message{v.Text, v.From, Channel("WHISPER")}

	default:
		fmt.Println(string(reply))
	}

	return true
}
