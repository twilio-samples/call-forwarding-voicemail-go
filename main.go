package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/ddymko/go-jsonerror"
	"github.com/joho/godotenv"
	naturaldate "github.com/tj/go-naturaldate"
	"github.com/twilio/twilio-go"
	twilioAPI "github.com/twilio/twilio-go/rest/api/v2010"
	"github.com/twilio/twilio-go/twiml"
)

// isDuringBusinessHours checks if the current time is within business hours
func isDuringBusinessHours(weekStart string, weekEnd string, dayStart int, dayEnd int) (bool, error) {
	now := time.Now()
	workWeekStart, err := naturaldate.Parse("last "+weekStart, now)
	if err != nil {
		return false, err
	}
	workWeekStart = workWeekStart.Add(time.Hour * time.Duration(dayStart))

	workWeekEnd, err := naturaldate.Parse("next "+weekEnd, now)
	if err != nil {
		return false, err
	}
	workWeekEnd = workWeekEnd.Add(time.Hour * time.Duration(dayEnd))

	workDayStart, err := naturaldate.Parse(strconv.Itoa(dayStart), now)
	if err != nil {
		return false, err
	}

	workDayEnd, err := naturaldate.Parse(strconv.Itoa(dayEnd), now)
	if err != nil {
		return false, err
	}

	return now.Before(workWeekStart) || now.After(workWeekEnd) || now.Before(workDayStart) || now.After(workDayEnd), nil
}

// getEnv get key environment variable if exist, otherwise return defaultValue
// copied from https://stackoverflow.com/a/40326580/222011
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func appError(w http.ResponseWriter, err error) {
	var error jsonerror.ErrorJSON
	error.AddError(jsonerror.ErrorComp{
		Detail: err.Error(),
		Code:   strconv.Itoa(http.StatusBadRequest),
		Title:  "Something went wrong",
		Status: http.StatusBadRequest,
	})
	http.Error(w, error.Error(), http.StatusBadRequest)
}

// handleCallRequest forwards incoming calls to a specified number during
// business hours; by default, business hours are Monday to Friday 8:00-18:00
// UTC.  Otherwise, it directs the call to voicemail. If the call is directed to
// voicemail, a message can be recorded and a link of the recording sent via SMS
// to the configured phone number.
func handleCallRequest(w http.ResponseWriter, r *http.Request) {
	workWeekStart := getEnv("WORK_WEEK_START", "Monday")
	workWeekEnd := getEnv("WORK_WEEK_END", "Friday")
	workDayStart, _ := strconv.Atoi(getEnv("WORK_DAY_START", "8"))
	workDayEnd, _ := strconv.Atoi(getEnv("WORK_DAY_END", "18"))

	duringBusinessHours, err := isDuringBusinessHours(workWeekStart, workWeekEnd, workDayStart, workDayEnd)
	if err != nil {
		appError(w, fmt.Errorf("could not determine if current time is within business hours. reason: %s", err))
		return
	}

	w.Header().Add("Content-Type", "application/xml")

	if !duringBusinessHours {
		record := &twiml.VoiceRecord{
			FinishOnKey:        "#",
			MaxLength:          "300",
			Timeout:            "10",
			Transcribe:         "true",
			TranscribeCallback: "/sms",
		}
		twimlResult, err := twiml.Voice([]twiml.Element{record})
		if err == nil {
			appError(w, fmt.Errorf("could not record voice call. reason: %s", err))
		}
		w.Write([]byte(twimlResult))
		return
	}

	dial := &twiml.VoiceDial{Number: os.Getenv("MY_PHONE_NUMBER")}
	say := &twiml.VoiceSay{Message: "Sorry, I was unable to redirect you. Goodbye."}
	twimlResult, err := twiml.Voice([]twiml.Element{dial, say})
	if err == nil {
		appError(w, fmt.Errorf("could not redirect call. reason: %s", err))
	}
	w.Write([]byte(twimlResult))
}

// sendVoiceRecording receives a POST request (from Twilio) with a text
// transcription of a voice recording which it then sends to the specified phone
// number via SMS.
func sendVoiceRecording(w http.ResponseWriter, r *http.Request) {
	client := twilio.NewRestClientWithParams(twilio.ClientParams{
		Username: os.Getenv("TWILIO_ACCOUNT_SID"),
		Password: os.Getenv("TWILIO_AUTH_TOKEN"),
	})
	params := &twilioAPI.CreateMessageParams{}
	params.SetTo(os.Getenv("MY_PHONE_NUMBER"))
	params.SetFrom(r.FormValue("from"))
	params.SetBody(r.FormValue("transcription_text"))

	resp, err := client.Api.CreateMessage(params)
	if err != nil {
		fmt.Println("Error sending SMS message: " + err.Error())
	}

	message := "The SMS with the voice recording transcript was sent successfully."
	if slices.Contains([]string{"cancelled", "failed", "undelivered"}, *resp.Status) {
		message = "Something went wrong sending the SMS with the voice recording transcript."
	}

	w.Write([]byte(message))
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /", handleCallRequest)
	mux.HandleFunc("POST /sms", sendVoiceRecording)

	log.Print("Starting server on :8080")
	err = http.ListenAndServe(":8080", mux)
	log.Fatal(err)
}
