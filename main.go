package main

import (
	"context"
	"database/sql"
	"encoding/json"

	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/heroiclabs/nakama-common/runtime"
)

type OneSignalSettings struct {
	isPushDisabled bool
	isSubscribed   bool
	pushToken      string
	userId         string
}

type UserTargetedOneSignalNotification struct {
	RecipientPlayerId string `json:"recipient_player_id"`
	Payload           string `json:"payload"`
	At                string `json:"at"`
	ChallengeSlug     string `json:"challenge_slug"`
}

type CancelOneSignalNotificationRequest struct {
	NotificationId string `json:"notification_id"`
}

func CancelOneSignalNotification(request CancelOneSignalNotificationRequest, logger runtime.Logger) ([]byte, error) {
	restApiKey := os.Getenv("ONE_SIGNAL_REST_API_KEY")
	appId := os.Getenv("ONE_SIGNAL_APP_ID")

	url := fmt.Sprintf("https://onesignal.com/api/v1/notifications/%v?app_id=%v", request.NotificationId, appId)

	req, err := http.NewRequest("DELETE", url, nil)

	if err != nil {
		return nil, err
	}

	req.Header.Add("Accept", "application/json")
	req.Header.Add("Authorization", fmt.Sprintf("Basic %v", restApiKey))
	req.Header.Add("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)

	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)

	if err != nil {
		return nil, err
	}
	logger.Debug("OneSingal API Response: %v", res)

	// logger.Debug("OneSingal API Body: %v", string(body))
	return body, nil

}

func SendOneSignalPushNotification(notification UserTargetedOneSignalNotification, logger runtime.Logger) ([]byte, error) {

	logger.Debug("notification data: %v", notification)
	restApiKey := os.Getenv("ONE_SIGNAL_REST_API_KEY")
	appId := os.Getenv("ONE_SIGNAL_APP_ID")

	url := "https://onesignal.com/api/v1/notifications"

	payload := strings.NewReader(fmt.Sprintf(`
	{
		"app_id": "%v",
		"include_player_ids": ["%v"],
		"contents": {
			"en": "%v"
		},
		"name": "REMINDER",
		"send_after": "%v"
	}
	`, appId, notification.RecipientPlayerId, notification.Payload, notification.At))

	logger.Debug("OneSingal API Response: %v", payload)

	req, err := http.NewRequest("POST", url, payload)

	if err != nil {
		return nil, err
	}

	req.Header.Add("Accept", "application/json")
	req.Header.Add("Authorization", fmt.Sprintf("Basic %v", restApiKey))
	req.Header.Add("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)

	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)

	if err != nil {
		return nil, err
	}
	logger.Debug("OneSingal API Response: %v", res)

	logger.Debug("OneSingal API Body: %v", string(body))
	return body, nil
}

func SaveNotificationId(notificationId string, userId string, challengeSlug string, logger runtime.Logger, db *sql.DB) error {

	saveNotificationIdStmt := `
	insert into push_notifications
		(id, user_id, challenge_slug)
	values
		($1, $2, $3); 
	`

	stmt, err := db.Prepare(saveNotificationIdStmt)

	if err != nil {
		return runtime.NewError("cant prepare stmt", 13)
	}

	if _, err := stmt.Exec(notificationId, userId, challengeSlug); err != nil {
		return runtime.NewError("api is unhappy", 13)
	}
	return nil
}

type ApiSuccessResponse struct {
	Id         string `json:"id"`
	Recipients string `json:"recipients\"`
	ExternalId string `json:"external_id\"`
}

func ScheduleOneSinalNotificaion(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {

	// "payload" is bytes sent by the client we'll JSON decode it.
	value := UserTargetedOneSignalNotification{}
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return "", runtime.NewError("unable to unmarshal payload", 13)
	}

	apiResponsebody, err := SendOneSignalPushNotification(value, logger)
	if err != nil {
		return "", runtime.NewError("api is unhappy", 13)
	}

	apiRes := ApiSuccessResponse{}
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return "", runtime.NewError("unable to unmarshal payload", 13)
	}
	userId := ctx.Value(runtime.RUNTIME_CTX_USER_ID).(string)
	if err := SaveNotificationId(apiRes.Id, userId, value.ChallengeSlug, logger, db); err != nil {
		return string(apiResponsebody), runtime.NewError("unable to save notification data", 13)
	}

	return string(apiResponsebody), nil
}

func UnscheduleOneSinalNotificaion(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	logger.Info("Payload: %s", payload)

	// "payload" is bytes sent by the client we'll JSON decode it.
	var value CancelOneSignalNotificationRequest
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return "", runtime.NewError("unable to unmarshal payload", 13)
	}

	response, err := json.Marshal(value)
	if err != nil {
		return "", runtime.NewError("unable to marshal payload", 13)
	}

	return string(response), nil
}

func PrepareTable(db *sql.DB, logger runtime.Logger) error {

	createTableQuery := `
	create table if not exists push_notifications
	(
	  id text primary key,
	  user_id uuid,
	  challenge_slug text
	
	);
	
	`
	_, err := db.Exec(createTableQuery)
	if err != nil {
		logger.Error("Error creating table: %v", err)
		return runtime.NewError("Error creating table", -1)
	}
	return nil
}

func InitModule(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, initializer runtime.Initializer) error {
	logger.Info("Go Plugin initialized!")
	PrepareTable(db, logger)

	if err := initializer.RegisterRpc("schedule_one_signal_notification", ScheduleOneSinalNotificaion); err != nil {
		logger.Error("Unable to register: %v", err)
		return err
	}

	if err := initializer.RegisterRpc("unschedule_one_signal_notification", UnscheduleOneSinalNotificaion); err != nil {
		logger.Error("Unable to register: %v", err)
		return err
	}
	return nil
}
