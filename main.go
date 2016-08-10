package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"gopkg.in/xmlpath.v2"
	"bytes"
	"strconv"
	"time"
	"strings"
	"flag"
	"net/url"
)

const (
	KEYWORD = "menu"
	TOMORROW = "tomorrow"
	CONVERSATION_ID = "<conversationId>"
)

type Settings struct {
	ServerEndpoint, AuthUrl, ApiUrl, BotId, BotSecret, ActivityEndpoint string
	Port, HttpPort int
}

type SkypeMsg struct {
	Type, Timestamp, Id, Text string
	From, To, Conversation AddressObject
}

type AddressObject struct {
	Id string `json:"id"`
	Name string `json:"name"`
}

type SkypeActivity struct {
	Type string `json:"type"`
	ChannelId string `json:"channelId"`
	From AddressObject `json:"from"`
	To AddressObject `json:"to"`
	Text string `json:"text"`
}

type Site struct {
	Name, Url string
	DayPaths []string
}

type Menu struct {
	menu map[string][]string
}

type AuthAnswer struct {
	Token_Type string
	Expires_In int
	Ext_Expires_In int
	Access_Token string
}

type BearerToken struct {
	Token string
	Timeout time.Time
}

func newMenu() *Menu {
	result := Menu{}
	result.menu = make(map[string][]string)
	return &result
}

var (
	settings Settings
	sites []Site
	bearer BearerToken
)

func main() {
	unencrypted := flag.Bool("http", false, "Starts webhook in regular HTTP instead of HTTPS")
	flag.Parse()

	fmt.Printf("http ", *unencrypted)

	settingsFile, err2 := os.Open("settings.json")
	sitesFile, err := os.Open("sites.json")

	if err != nil || err2 != nil {
		log.Fatal(err)
	}

	settings = loadSettings(settingsFile)
	sites = loadSites(sitesFile)

	settingsFile.Close()
	sitesFile.Close()

	fmt.Println(settings)
	//settings.ApiUrl = ""
	fmt.Println(sites)

	http.HandleFunc(settings.ServerEndpoint, RequestHandler)

	err = nil

	if (*unencrypted) {
		err = http.ListenAndServe(":" + strconv.Itoa(settings.HttpPort), nil)
	} else {
		err = http.ListenAndServeTLS(":" + strconv.Itoa(settings.Port), "cert.crt", "key.key", nil)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func getBearerToken(settings Settings) BearerToken {
	bearer := BearerToken{}

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Add("client_id", settings.BotId)
	data.Add("client_secret", settings.BotSecret)
	data.Add("scope", "https://graph.microsoft.com/.default")

	resp, err := http.PostForm(settings.AuthUrl, data)

	if err != nil {
		log.Fatal(err)
	}

	msg, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}

	resp.Body.Close()

	var authAnswer AuthAnswer

	err = json.Unmarshal(msg, &authAnswer)

	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(authAnswer)

	bearer.Timeout = time.Now().Add(time.Duration(authAnswer.Expires_In)*time.Second)
	bearer.Token = authAnswer.Access_Token

	return bearer
}

func RequestHandler(w http.ResponseWriter, r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%s", body)

	msg := decodeMessage(body)
	fmt.Println("Msg: %s", msg)

	w.WriteHeader(http.StatusCreated)

	if strings.HasPrefix(strings.ToLower(msg.Text), KEYWORD) {
		tomorrow := false;
		if strings.Contains(strings.ToLower(msg.Text), TOMORROW) {
			tomorrow = true;
		}
		sendAnswer(msg.To, msg.From, generateAnswer(tomorrow), msg.Conversation.Id)
	}
}

func sendAnswer(from AddressObject, to AddressObject, msg string, convId string) {
	fmt.Print(msg)

	fmt.Println(bearer.Timeout)

	if bearer.Timeout.Before(time.Now()) {
		log.Print("Refreshing bearer token")
		bearer = getBearerToken(settings)
	}

	activity := SkypeActivity{}
	activity.Type = "message"
	activity.ChannelId = "skype"
	activity.From = from
	activity.To = to
	activity.Text = msg

	client := &http.Client{}

	url := strings.Replace(settings.ApiUrl + settings.ActivityEndpoint, CONVERSATION_ID, convId, 1)
	fmt.Println("Send msg to ", url)

	activityMsg, err := json.Marshal(activity)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(activityMsg))

	if err != nil {
		log.Fatal(err)
	}

	req.Header.Add("Authorization", "Bearer " + bearer.Token)
	req.Header.Add("Content-Type", "application/json;charset=utf-8");

	resp, err := client.Do(req)

	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(resp)
}

func generateAnswer(tomorrow bool) string {

	fmt.Println("nomnom go!")

	menu := newMenu()

	for _, site := range sites {
		populateMenu(menu, site)
	}

	weekday := (int)(time.Now().Weekday())
	if tomorrow {
		weekday++
	}
	weekday = weekday%7

	empty := true

	output := ""

	for _, site := range sites {
		localMenu := menu.menu[site.Name + "_" + strconv.Itoa(weekday)]
		if localMenu != nil {
			empty = false
			output = fmt.Sprintf("%s#= **%s** =\n", output, site.Name)
			for index, menuline := range localMenu {
				prefix := ""
				if len(localMenu) > 1 {
					prefix = strconv.Itoa(index+1) + " "
				}
				output = fmt.Sprintf("%s ### %s\n", output, prefix + menuline)
			}
		}
	}

	if empty {
		if tomorrow {
			output = fmt.Sprintln("No menus tomorrow.")
		} else {
			output = fmt.Sprintln("No menus today.")
		}
	}
	return output
}

func populateMenu(menu *Menu, site Site) {

	resp, err := http.Get(site.Url)
	if err != nil {
		log.Fatal(err)
	}

	body, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		log.Fatal(err)
	}

	reader := bytes.NewReader(body)
	rootNode, err := xmlpath.ParseHTML(reader)

	for index, dayPath := range site.DayPaths {

		if len(dayPath) == 0 {
			continue
		}

		path, err2 := xmlpath.Compile(dayPath)

		if err != nil || err2 != nil {
			log.Fatal(err, err2)
		}

		iter := path.Iter(rootNode)
		key := site.Name + "_" + strconv.Itoa(index)
		for iter.Next() {
			localMenu := menu.menu[key]

			if localMenu == nil {
				localMenu = []string{}
			}
			localMenu = append(localMenu, strings.TrimSpace(iter.Node().String()))
			menu.menu[key] = localMenu
		}
	}

}

func loadSettings(file *os.File) Settings {
	var settings Settings
	dec := json.NewDecoder(file)
	err := dec.Decode(&settings)
	if err != nil {
		log.Fatal(err)
	}
	return settings
}

func loadSites(file *os.File) []Site {
	sites := []Site{}

	dec := json.NewDecoder(file)
	T, err := dec.Token()
	T = T.(json.Delim)
	if err != nil {
		log.Fatal(err)
	}

	for dec.More() {
		var site Site
		err := dec.Decode(&site)
		if err != nil {
			log.Fatal("Could not read json: ", err)
		}
		sites = append(sites, site)
	}
	return sites
}

func decodeMessage(msg []byte) SkypeMsg {
	var skypeMsg SkypeMsg
	err := json.Unmarshal(msg, &skypeMsg)
	if err != nil {
		log.Fatal(err)
	}
	return skypeMsg
}
