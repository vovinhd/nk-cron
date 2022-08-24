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
	"time"

	"github.com/go-co-op/gocron"
	"github.com/heroiclabs/nakama-common/runtime"
)

type OneSignalSettings struct {
	isPushDisabled bool
	isSubscribed   bool
	pushToken      string
	userId         string
}

type UserTargetedOneSignalNotification struct {
	recipient_player_id string
	campaing_name       string
	payload             string
}

func SendOneSignalPushNotification(notification UserTargetedOneSignalNotification, logger runtime.Logger) error {

	restApiKey := os.Getenv("ONE_SIGNAL_REST_API_KEY")
	appId := os.Getenv("ONE_SIGNAL_APP_ID")

	url := "https://onesignal.com/api/v1/notifications"

	payload := strings.NewReader(fmt.Sprintf(`
	{
		"app_id": "%v"
		"include_player_ids": ["%v"],
		"contents": {
			"en": "%v"
		}
		"name": "%v"
	}
	`, appId, notification.recipient_player_id, notification.payload, notification.campaing_name))

	req, err := http.NewRequest("POST", url, payload)

	if err != nil {
		return err
	}
	req.Header.Add("Accept", "application/json")

	req.Header.Add("Authorization", fmt.Sprintf("Basic %v", restApiKey))

	req.Header.Add("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)

	if err != nil {
		return err
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)

	if err != nil {
		return err
	}
	logger.Debug("OneSingal API Response: %v", res)

	logger.Debug("OneSingal API Body: %v", string(body))
	return nil
}

func SendNotificationsTask(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, initializer runtime.Initializer) error {

	var (
		u_id                string
		challenge_slug      string
		notificationDate    string
		notificationSetting string
		send_at             time.Time
		received            bool
		oneSignalSettings   string
	)

	query := `
	select challengeData.user_id,
	challengeData.challenge_slug,
	notificationDate,
	coalesce(notificationFequencySetting, 'all')                    as freqency_setting,
	coalesce(sendNotifications.send_at, '1970-01-01T00:01' :: date) as send_at,
	coalesce(sendNotifications.received, false)                     as received,
	coalesce(oneSignalSetting, '{}' :: jsonb)                       as oneSignalSettings
from (select user_id,
		  value ->> 'challengeSlug'  as challenge_slug,
		  value ->> 'nextCheckpoint' as notificationDate
   from storage
   where collection = 'challenge-interactions'
	 and value ->> 'type' = 'accept'
	 and value ->> 'nextCheckpoint' is not NULL)
	  as challengeData
	  left join
  (select user_id,
		  value ->> 'frequency' as notificationFequencySetting
   from storage
   where collection = 'notification-settings'
	 and key = 'notification-freq') as notificationSettings
  on challengeData.user_id = notificationSettings.user_id
	  left join
  (select user_id,
		  value as oneSignalSetting
   from storage
   where collection = 'notification-settings'
	 and key = 'onesignal-settings'
	 and value ->> 'isPushDisabled' = 'false') as oneSignalSettings
  on challengeData.user_id = oneSignalSettings.user_id
	  left join

  (select recipient_id, challenge_slug, send_at, received
   from push_notifications) as sendNotifications
  on recipient_id = challengeData.user_id and sendNotifications.challenge_slug = challengeData.challenge_slug;
= challengeData.user_id and sendNotifications.challenge_slug = challengeData.challenge_slug;
`

	rows, err := db.Query(query)
	if err != nil {
		logger.Error("DB Error: %v", err)
	}

	defer rows.Close()

	for rows.Next() {
		err := rows.Scan(&u_id, &challenge_slug, &notificationDate, &notificationSetting, &send_at, &received, &oneSignalSettings)
		if err != nil {
			logger.Error("DB Error: %v", err)
		}
		// logger.Info("%v %v %v %v %v %v", u_id, challenge_slug, notificationDate, notificationSetting, send_at, received)
		if !received {

			layout := "2006-01-02T15:04"
			date, err := time.Parse(layout, notificationDate)
			if err != nil {
				logger.Error("Cant parse date: %v", err)
				break
			}
			SendNotification(u_id, challenge_slug, date, notificationSetting, oneSignalSettings, send_at, received, ctx, logger, db, nk)
		} else {
			logger.Debug("notification already send!")

		}
	}

	err = rows.Err()

	if err != nil {
		logger.Error("DB Error: %v", err)
	}

	return nil
}

func SendPushNotification(u_id string, challenge_slug string, notificationDate time.Time, notificationSetting string, oneSignalSetting string, send_at time.Time, received bool, ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule) error {

	var _settings OneSignalSettings

	if err := json.Unmarshal([]byte(oneSignalSetting), &_settings); err != nil {
		logger.Error("reading oneSignalSetting from db: %v", err)
		return err
	}

	if _settings.isPushDisabled {
		return nil
	}

	if _settings.pushToken != "" {

		payload := UserTargetedOneSignalNotification{
			recipient_player_id: _settings.userId,
			campaing_name:       "REMINDER",
			payload:             challenge_slug,
		}

		SendOneSignalPushNotification(payload, logger)

	}

	return nil
}
func SendNotification(u_id string, challenge_slug string, notificationDate time.Time, notificationSetting string, oneSignalSetting string, send_at time.Time, received bool, ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule) error {
	logger.Info("Sending notification %v %v %v %v %v %v", u_id, challenge_slug, notificationDate, notificationSetting, send_at, received)

	err := nk.NotificationSend(ctx, u_id, challenge_slug, nil, 5000, "00000000-0000-0000-0000-000000000000", true)

	SendPushNotification(u_id, challenge_slug, notificationDate, notificationSetting, oneSignalSetting, send_at, received, ctx, logger, db, nk)

	if err != nil {
		logger.Error("Couldn't send internal notification: %v", err)
		return err
	}

	notificationUpdateQuery := `
	INSERT INTO push_notifications (id, challenge_slug, notification_text, recipient_id, send_at, received, status) VALUES (DEFAULT, $1, $2, $3, $4, $5, $6)
	`
	stmt, err := db.Prepare(notificationUpdateQuery)
	if err != nil {
		logger.Error("stmt Error: %v", err)
		return err
	}

	_, err = stmt.Exec(challenge_slug, challenge_slug, u_id, time.Now(), true, "")

	if err != nil {
		logger.Error("DB Error: %v", err)
		return err
	}
	return nil
}

func Task(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, initializer runtime.Initializer) error {
	logger.Info("Hello World!")
	return nil
}

func InitScheduler(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, initializer runtime.Initializer) error {
	sched := gocron.NewScheduler(time.UTC)
	_, err := sched.Every(5).Second().Do(SendNotificationsTask, ctx, logger, db, nk, initializer)
	if err != nil {
		logger.Error("Error creating notification task scheduler: %v", err)
	} else {
		logger.Info("Starting notification task scheduler!")
		sched.StartAsync()
	}
	return nil
}

func PrepareTable(db *sql.DB, logger runtime.Logger) error {

	createTableQuery := `
	create table if not exists push_notifications
	(
		id                serial primary key,
		challenge_slug    text,
		notification_text text,
		recipient_id      uuid not null,
		send_at           date not null,
		received          bool,
		status            text
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
	InitScheduler(ctx, logger, db, nk, initializer)
	return nil
}
