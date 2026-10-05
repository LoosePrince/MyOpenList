package lanzou

import "testing"

func TestParseFnPage(t *testing.T) {
	page := `
		<script>
			var wp_sign = 'signed-value';
			var ajaxdata = 'ajax-value';
			var kdns = 0;
			var domain1 = 'https://apifile.woozooo.com/ajaxfile.php?file=164520377';
		</script>`

	gotURL, gotForm, err := parseFnPage(page)
	if err != nil {
		t.Fatalf("parseFnPage() error = %v", err)
	}
	if gotURL != "https://apifile.woozooo.com/ajaxfile.php?file=164520377" {
		t.Fatalf("parseFnPage() URL = %q", gotURL)
	}
	wantForm := map[string]string{
		"action":     "downprocess",
		"websignkey": "ajax-value",
		"signs":      "ajax-value",
		"sign":       "signed-value",
		"websign":    "",
		"kd":         "0",
		"ves":        "1",
	}
	for key, want := range wantForm {
		if gotForm[key] != want {
			t.Errorf("parseFnPage() form[%q] = %q, want %q", key, gotForm[key], want)
		}
	}
}

func TestParseFnPageSupportsDoubleQuotesAndDeclarations(t *testing.T) {
	page := `const wp_sign = "signed";
		let ajaxdata = "ajax";
		const kdns = 1;
		let domain2 = "https://apifile.lanzouw.com/ajaxfile.php?file=123";`

	gotURL, gotForm, err := parseFnPage(page)
	if err != nil {
		t.Fatalf("parseFnPage() error = %v", err)
	}
	if gotURL != "https://apifile.lanzouw.com/ajaxfile.php?file=123" {
		t.Fatalf("parseFnPage() URL = %q", gotURL)
	}
	if gotForm["sign"] != "signed" || gotForm["websignkey"] != "ajax" {
		t.Fatalf("parseFnPage() form = %#v", gotForm)
	}
}

func TestFindFileID(t *testing.T) {
	page := `$.post('/ajaxm.php?file=12345', { 'ves': 1 });`
	matches := findFileIDReg.FindStringSubmatch(page)
	if len(matches) != 2 || matches[1] != "/ajaxm.php?file=12345" {
		t.Fatalf("findFileIDReg matches = %#v", matches)
	}
}

func TestResolveSharePageURL(t *testing.T) {
	if got := resolveSharePageURL("https://pan.lanzoui.com", "/fn?token"); got != "https://pan.lanzoui.com/fn?token" {
		t.Fatalf("resolveSharePageURL() = %q", got)
	}
	absolute := "https://apifile.woozooo.com/ajaxfile.php?file=1"
	if got := resolveSharePageURL("https://pan.lanzoui.com", absolute); got != absolute {
		t.Fatalf("resolveSharePageURL() changed absolute URL to %q", got)
	}
}
