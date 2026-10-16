package main

import (
	"strings"
	"testing"
)

const tableFixture = `<!doctype html><html><body>
<h1>ПРОВЕРКА НА ЦЕНИ</h1><p>Прикажи 1 - 2 од 3 артикли</p><p>Датум и време на последно ажурирање на цените: 10/10/2026 04:00</p>
<table><thead><tr><th>Назив на стока-производ</th><th>Продажна цена</th><th>Единечна цена</th><th>Опис на стока</th><th>Достапност во продажен објект</th><th>Редовна цена</th></tr></thead>
<tbody><tr><td>МЕГЛЕ МЛЕКО 3.2% 1Л</td><td>79 ден.</td><td>100мл = 7.9 ден.</td><td>ТРАЈНО МЛЕКО</td><td>Да</td><td>85 ден.</td></tr>
<tr><td>ЛЕБ БЕЛ 500Г</td><td>45 ден.</td><td>100г = 9 ден.</td><td>ЛЕБ</td><td>Да</td><td>45 ден.</td></tr></tbody></table></body></html>`

func TestHTMLParse(t *testing.T) {
	p, e := ParsePriceTable([]byte(tableFixture))
	if e != nil {
		t.Fatal(e)
	}
	if p.Total != 3 || len(p.Rows) != 2 || p.Rows[0].Name != "МЕГЛЕ МЛЕКО 3.2% 1Л" || p.Rows[0].Price != "79 ден." || p.UpdatedAt != "10/10/2026" {
		t.Fatalf("unexpected parsed page: %#v", p)
	}
}
func TestCSV(t *testing.T) {
	input := "name,price_mkd,description,available,source_updated_at\n\"TEST МЛЕКО 3.2% 1Л\",70.00,ТРАЈНО МЛЕКО,true,2026-10-11\n"
	rows, e := ReadCSV(strings.NewReader(input))
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 1 || rows[0].UpdatedAt != "2026-10-11" {
		t.Fatalf("rows %#v", rows)
	}
}
