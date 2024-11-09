package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

func reportHandler(w http.ResponseWriter, r *http.Request) {
	if !checkUserAuthorized(r) {
		respondWithUnauthorized(w, r)
		return
	}

	profileCount := 0
	var lastreport time.Time
	err := dbpool.QueryRow(r.Context(), `SELECT a.last_report, (SELECT count(*) FROM identities WHERE account = a.id) FROM accounts as a WHERE username = $1`, sessionGetUsername(r)).Scan(&lastreport, &profileCount)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Error occured, contact administrator"})
		log.Println(err)
		return
	}
	if profileCount == 0 {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "You must link in-game profile first to be able to report others"})
		return
	}
	if time.Since(lastreport).Hours() < 12 {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "You can submit only one report in 12 hours"})
		return
	}

	err = r.ParseForm()
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Invalid form"})
		return
	}

	iViolation := r.FormValue("violation")
	iViolationTime := r.FormValue("violationTime")
	iOffender := r.FormValue("offender")
	iComment := r.FormValue("comment")

	if r.FormValue("agree1") != "on" || r.FormValue("agree2") != "on" || r.FormValue("agree3") != "on" {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "You must understand reporting rules"})
		return
	}

	if iViolation == "" || iOffender == "" || iComment == "" || iViolationTime == "" {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Empty fields not allowed"})
		return
	}
	if len(iViolation) > 80 || len(iViolationTime) > 24 || len(iOffender) > 300 || len(iComment) > 1500 {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "One or more fields are exeeding it's length"})
		return
	}

	_, err = dbpool.Exec(r.Context(), `INSERT INTO reports (reporter, violation, violationtime, offender, comment) VALUES ($1, $2, $3, $4, $5)`,
		sessionGetUsername(r), iViolation, iViolationTime, iOffender, iComment)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Error occured, contact administrator"})
		log.Println(err)
		return
	}

	_, err = dbpool.Exec(r.Context(), `UPDATE accounts SET last_report = now() WHERE username = $1`, sessionGetUsername(r))
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Error occured, contact administrator"})
		log.Println(err)
		return
	}

	basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Report successfully submitted."})
	sendReportWebhook(fmt.Sprintf("User `%s` reported violations `%s` of a player `%s` at `%s` \nComment:\n```\n%s\n```",
		escapeBacktick(sessionGetUsername(r)),
		escapeBacktick(r.FormValue("violation")),
		escapeBacktick(r.FormValue("offender")),
		escapeBacktick(r.FormValue("violationTime")),
		escapeBacktick(r.FormValue("comment"))))
}

func sendReportWebhook(content string) error {
	return sendWebhook(cfg.GetDSString("", "webhooks", "reports"), content)
}
