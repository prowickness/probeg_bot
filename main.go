package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Bot struct {
	client  *http.Client
	baseURL string
}

func NewBot(baseURL string) (*Bot, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Jar:     jar,
		Timeout: 15 * time.Second,
	}

	return &Bot{
		client:  client,
		baseURL: baseURL,
	}, nil
}

func (b *Bot) Login(email, password string) error {
	loginURL := b.baseURL + "/index.php?r=account%2Flogin"
	getReq, err := http.NewRequest(http.MethodGet, loginURL, nil)
	if err != nil {
		return err
	}
	getReq.Header.Set("User-Agent", "Mozilla/5.0")
	getResp, err := b.client.Do(getReq)
	if err != nil {
		return err
	}
	defer getResp.Body.Close()
	pageBody, err := io.ReadAll(getResp.Body)
	if err != nil {
		return err
	}
	if getResp.StatusCode != http.StatusOK {
		return fmt.Errorf("login page failed: status=%d body=%s", getResp.StatusCode, string(pageBody))
	}

	csrf := extractCSRF(string(pageBody))
	form := url.Values{}

	form.Set("LoginForm[username]", email)
	form.Set("LoginForm[password]", password)
	if csrf != "" {
		form.Set("_csrf", csrf)
	}
	postReq, err := http.NewRequest(
		http.MethodPost,
		loginURL,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return err
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("User-Agent", "Mozilla/5.0")
	postReq.Header.Set("Referer", loginURL)
	postResp, err := b.client.Do(postReq)
	if err != nil {
		return err
	}
	defer postResp.Body.Close()
	postBody, err := io.ReadAll(postResp.Body)
	if err != nil {
		return err
	}
	if postResp.StatusCode != http.StatusOK && postResp.StatusCode != http.StatusFound {
		return fmt.Errorf("login post failed: status=%d body=%s", postResp.StatusCode, string(postBody))
	}

	if strings.Contains(string(postBody), "Войти") &&
		strings.Contains(string(postBody), "Пароль") {
		return fmt.Errorf("login probably failed: still on login page")
	}
	return nil
}

func extractCSRF(html string) string {
	re := regexp.MustCompile(`name="_csrf"\s+value="([^"]+)"`)
	matches := re.FindStringSubmatch(html)
	if len(matches) >= 2 {
		return matches[1]
	}

	re = regexp.MustCompile(`name="csrf-token"\s+content="([^"]+)"`)
	matches = re.FindStringSubmatch(html)
	if len(matches) >= 2 {
		return matches[1]
	}

	return ""
}

type RaiseResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func (b *Bot) RaiseAd(adID string) error {
	raiseURL := b.baseURL + "/index.php?r=action/up"

	form := url.Values{}
	form.Set("id", adID)

	body := form.Encode()

	req, err := http.NewRequest(
		http.MethodPost,
		raiseURL,
		strings.NewReader(body),
	)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Origin", b.baseURL)
	req.Header.Set("Referer", b.baseURL+"/index.php?r=account&category_id=0&order=1&isimages=0")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	log.Println("raise status:", resp.StatusCode)
	log.Println("raise body:", string(respBody))

	var rr RaiseResponse
	if err := json.Unmarshal(respBody, &rr); err != nil {
		return fmt.Errorf("raise returned non-json: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("raise http failed: status=%d message=%s", resp.StatusCode, rr.Message)
	}

	if rr.Status != "success" {
		return fmt.Errorf("raise rejected: %s", rr.Message)
	}

	log.Printf("ad raised successfully: ad_id=%s message=%s", adID, rr.Message)
	return nil
}

func (b *Bot) CheckAuth() error {
	accountURL := b.baseURL + "/index.php?r=account%2Findex"

	req, err := http.NewRequest(http.MethodGet, accountURL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", b.baseURL+"/index.php?r=account%2Flogin")

	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	html := string(body)

	fmt.Println("check auth status:", resp.StatusCode)
	fmt.Println("check auth final url:", resp.Request.URL.String())

	fmt.Println(string(body))

	if strings.Contains(html, "Войти") &&
		(strings.Contains(html, "Пароль") || strings.Contains(html, "Регистрация")) {
		return fmt.Errorf("not authorized: login page or guest block detected")
	}

	if strings.Contains(html, "Выйти") ||
		strings.Contains(html, "Мои объявления") ||
		strings.Contains(html, "Подать объявление") {
		return nil
	}

	return fmt.Errorf("cannot determine auth state")
}

func (b *Bot) CheckMainPage(idsToCheck [4]string) (bool, error) {
	mainPageUrl := "https://board.orsk.ru/index.php?r=category&category_id=7"
	resp, err := b.client.Get(mainPageUrl)

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		fmt.Println(err)
		return false, err
	}

	adsListBlock := doc.Find(".container-items")

	if adsListBlock.Length() == 0 {
		fmt.Print("Ничего не было найдено")
		return false, errors.New("Nothing was found")
	}

	first := adsListBlock.First()

	stopAfterFour := 0
	topIDs := make([]string, 0)
	first.ChildrenFiltered("div").Each(func(i int, s *goquery.Selection) {
		if stopAfterFour >= 4 {
			return
		}

		dataKey, exists := s.Attr("data-key")
		topIDs = append(topIDs, dataKey)
		fmt.Println(dataKey, exists)
		stopAfterFour++
	})

	allFour := 0
	for _, valCheck := range idsToCheck {
		for _, valForCheck := range topIDs {
			if valCheck == valForCheck {
				allFour++
			}
		}
	}

	if allFour < 4 {
		return true, nil
	}

	resp.Body.Close()

	return false, nil
}

func main() {
	email := "orsk_avto2"
	password := "oavt06062023"

	locationTime, err := time.LoadLocation("Asia/Yekaterinburg")

	adIDs := [4]string{
		"3743393",
		"3743390",
		"3743389",
		"3743387",
	}

	bot, err := NewBot("https://board.orsk.ru")
	if err != nil {
		log.Fatal(err)
	}

	err = bot.Login(email, password)

	if err != nil {
		log.Println(time.Now().In(locationTime), "Login - ", err)
	}

	if err != nil {
		log.Println("Not correct time location")
	}
	timeToAct := time.Now().In(locationTime)
	ticker := time.NewTicker(60 * time.Minute)

	for range ticker.C {
		timeToAct = time.Now().In(locationTime)
		if timeToAct.Hour() >= 10 && timeToAct.Hour() < 19 {
			err := bot.CheckAuth()
			if err != nil {
				log.Println(time.Now().In(locationTime), "CheckAuth - ", err)
				err := bot.Login(email, password)
				if err != nil {
					log.Println(time.Now().In(locationTime), "Login - ", err)
					continue
				}
			}

			checkMainPage, err := bot.CheckMainPage(adIDs)
			if err != nil {
				log.Println(time.Now().In(locationTime), "CheckMainPage - ", err)
				continue
			}

			if checkMainPage {
				for _, id := range adIDs {
					err := bot.RaiseAd(id)
					if err != nil {
						log.Println(time.Now().In(locationTime), "RaiseAd - ", err)
						continue
					}
					log.Println(time.Now().In(locationTime), "RaiseAd (succeed) - ", id)
				}
			}
		}
	}

	// 200 поднятий есть
	// до конца мая 21 день
	// -> 9 поднятий в сутки все они должны быть сделаны в промежуток
	// с 10 до 19 это 540 минут, каждые 60 минут поднимать объявление
}
