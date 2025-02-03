package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/maxsupermanhd/go-wz/lobby"
)

const (
	lobbyHistoryMax = 50
)

type LobbyRoomPretty struct {
	GameID            uint32
	GameName          string
	MapName           string
	HostName          string
	Version           string
	Private           bool
	Pure              bool
	MaxPlayers        uint32
	CurrentPlayers    uint32
	MaxSpectators     int
	CurrentSpectators int
	LastSeen          int64
	History           bool
}

func lobbyRoomPrettyfy(room lobby.LobbyRoom) LobbyRoomPretty {
	return LobbyRoomPretty{
		GameID:            room.GameID,
		GameName:          string(room.GameName[:bytes.IndexByte(room.GameName[:], 0)]),
		MapName:           string(room.MapName[:bytes.IndexByte(room.MapName[:], 0)]),
		HostName:          string(room.HostName[:bytes.IndexByte(room.HostName[:], 0)]),
		Version:           string(room.Version[:bytes.IndexByte(room.Version[:], 0)]),
		Private:           btoi(room.Private),
		Pure:              btoi(room.Pure),
		MaxPlayers:        room.MaxPlayers,
		CurrentPlayers:    room.CurrentPlayers,
		MaxSpectators:     int(room.DWFlags[1] & 0xFFFF),
		CurrentSpectators: int(room.DWFlags[1] >> 16),
		LastSeen:          time.Now().Unix(),
		History:           false,
	}
}

type LobbyResponsePretty struct {
	lobby.LobbyResponse
	prettyRooms []LobbyRoomPretty
}

func lobbyLookup() (ret LobbyResponsePretty) {
	lookup, err := lobby.LobbyLookup()
	ret = LobbyResponsePretty{
		LobbyResponse: lookup,
		prettyRooms:   []LobbyRoomPretty{},
	}
	if err != nil {
		log.Printf("Error reading lobby: %s", err)
		return
	}
	for _, v := range lookup.Rooms {
		if lobbyIgnores(string(v.HostIP[:bytes.IndexByte(v.HostIP[:], 0)])) {
			continue
		}
		ret.prettyRooms = append(ret.prettyRooms, lobbyRoomPrettyfy(v))
	}
	return
}

func lobbyPoller() {
	lobbyHistory := []LobbyRoomPretty{}
	previousLookup := []LobbyRoomPretty{}
	lobbyHistoryKeepPath := cfg.GetDString("", "lobbyHistoryPath")
	if lobbyHistoryKeepPath != "" {
		saveHistoryBytes, err := os.ReadFile(lobbyHistoryKeepPath)
		if err == nil {
			err = json.Unmarshal(saveHistoryBytes, &lobbyHistory)
			if err != nil {
				log.Println("error unmarshling history ", err)
			}
		} else {
			if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrPermission) {
				log.Println("error reading history", err)
			}
		}
		log.Println("restored lobby history")
	}
	for {
		lookup := lobbyLookup()
		for _, vv := range previousLookup {
			found := false
			for _, v := range lookup.prettyRooms {
				if v.GameID == vv.GameID {
					found = true
					break
				}
			}
			if !found {
				vv.History = true
				lobbyHistory = append([]LobbyRoomPretty{vv}, lobbyHistory...)
			}
		}
		if len(lobbyHistory) > lobbyHistoryMax {
			lobbyHistory = lobbyHistory[:lobbyHistoryMax]
		}
		if lobbyHistoryKeepPath != "" {
			saveHistoryBytes, err := json.MarshalIndent(lobbyHistory, "", "\t")
			if err != nil {
				log.Println("failed to marshal lobby history", err)
			} else {
				err = os.WriteFile(lobbyHistoryKeepPath, saveHistoryBytes, 0644)
				if err != nil {
					log.Println("failed to save lobby history", err)
				}
			}
		}
		previousLookup = lookup.prettyRooms
		LobbyWSHub.clientsLock.Lock()
		watchers := len(LobbyWSHub.clients)
		LobbyWSHub.clientsLock.Unlock()
		WSLobbyUpdateLobby(map[string]any{
			"Rooms":    append(lookup.prettyRooms, lobbyHistory...),
			"MOTD":     lookup.MOTD,
			"Watching": watchers,
		})
		time.Sleep(1 * time.Second)
	}
}

var (
	lobbyLastRequest         = map[string]time.Time{}
	lobbyLastRequestLock     sync.Mutex
	lobbyTooManyRequestsTime = int64(7) // seconds
)

func lobbyHandler(w http.ResponseWriter, r *http.Request) {
	u := sessionGetUsername(r)
	if len(u) > 0 && u != "Flex seal" {
		lobbyLastRequestLock.Lock()
		l, ok := lobbyLastRequest[u]
		lobbyLastRequest[u] = time.Now()
		if ok {
			if l.Unix()+lobbyTooManyRequestsTime > time.Now().Unix() {
				basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Too many requests. Please do not spam page refresh. Authorized accounts have automatic lobby refresh every second.", "msgred": true})
				lobbyLastRequestLock.Unlock()
				return
			}
		}
		lobbyLastRequestLock.Unlock()
	}
	// s, reqres := RequestHosters()
	// var rooms []any
	// if s {
	// 	json.Unmarshal([]byte(reqres), &rooms)
	// }
	// basicLayoutLookupRespond("lobby", w, r, map[string]any{"Lobby": LobbyLookup(), "Hoster": rooms})
	lr := lobbyLookup()
	basicLayoutLookupRespond("lobby", w, r, map[string]any{"Lobby": map[string]any{
		"Rooms": lr.prettyRooms,
		"MOTD":  lr.MOTD,
	}})
}

func lobbyIgnores(ip string) bool {
	for _, i := range lobbyIgnoreIPS {
		if i.MatchString(ip) {
			log.Printf("Lobby ignores %q because %q", ip, i.String())
			return true
		}
	}
	return false
}

var lobbyIgnoreIPS []*regexp.Regexp

func loadLobbyIgnores(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fi, fn, ok := strings.Cut(scanner.Text(), " ")
		if !ok || fi == "" {
			continue
		}
		r, err := regexp.Compile(fi)
		if err != nil {
			log.Printf("Failed to compile regex %q (%s): %s", fi, fn, err)
		}
		lobbyIgnoreIPS = append(lobbyIgnoreIPS, r)
	}
	return scanner.Err()
}
