package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/niklasfasching/x/soup"
	"golang.org/x/net/publicsuffix"
)

func verordnungen(dir string) error {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return err
	}

	client, form := &http.Client{Jar: jar}, url.Values{}
	form.Set("search[art_vorschrift][]", "landesrecht")
	form.Set("search[fundstelle_medium]", "0") // Gesetze und Verordnungen
	// bravors.brandenburg.de/de/vorschriften_fundstellennachweis_gesetzte_und_verordnungen_sachgebietlich
	// 791 = Naturschutz und Landschaftspflege
	form.Set("search[sachgebietsnr_1]", "791")
	res, err := client.PostForm("https://bravors.brandenburg.de/de/vorschriften_erweiterte_suche", form)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	doc, err := soup.Parse(res.Body)
	if err != nil {
		return err
	}
	head := doc.First(".ergebnisliste_kopf").TrimmedText()
	m := regexp.MustCompile(`(\d+) Treffern gefunden`).FindStringSubmatch(head)
	if m == nil {
		return fmt.Errorf("bad header: '%s'", head)
	}
	n, _ := strconv.Atoi(m[1])
	pages := (n + 10) / 10
	for i := 1; i <= pages; i++ {
		url := fmt.Sprintf(`https://bravors.brandenburg.de/de/vorschriften_erweiterte_suche/ergebnis/page/%d`, i)
		log.Println(n, pages, url)
		doc, err := soup.Load(client, url)
		if err != nil {
			return fmt.Errorf("%s: %w", url, err)
		}
		for _, a := range doc.All("dt .link a") {
			if err := scrapePage(dir, "https://bravors.brandenburg.de"+a.Attribute("href")); err != nil {
				return err
			}
		}
	}
	return nil
}

func scrapePage(dir, url string) error {
	name := filepath.Base(url)
	filename := filepath.Join(dir, name+".txt")
	if err := os.MkdirAll(filepath.Dir(filename), os.ModePerm); err != nil {
		return err
	}
	if _, err := os.Stat(filename); err == nil {
		return nil
	}
	doc, err := soup.Load(http.DefaultClient, url)
	if err != nil {
		return err
	}
	title := doc.First(".reiterbox_innen_kopf strong").TrimmedText()
	body := doc.First(".reiterbox_innen_text").TrimmedText()
	return os.WriteFile(filename, []byte(title+"\n\n"+body), 0644)
}
