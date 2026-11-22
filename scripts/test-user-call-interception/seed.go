//go:build ignore

package main

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"time"
)

func must(e error) {
	if e != nil {
		// Driver errors may interpolate credentials; never emit the raw value.
		fmt.Fprintln(os.Stderr, "isolated synthetic fixture operation failed")
		os.Exit(1)
	}
}
func random() string {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	must(e)
	return hex.EncodeToString(b)
}
func main() {
	dsn := os.Getenv("SQL_DSN")
	if dsn != "postgres://interception_test@127.0.0.1:55883/interception_runtime?sslmode=disable" {
		panic("isolated fixture DSN required")
	}
	db, e := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	must(e)
	model.DB = db
	model.LOG_DB = db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RedisEnabled = false
	common.CryptoSecret = os.Getenv("SESSION_SECRET")
	accounts := map[string]any{}
	for _, s := range []struct {
		Name     string
		ID, Role int
	}{{"root", 4101, 100}, {"admin", 4102, 10}, {"target", 4201, 1}, {"other", 4202, 1}} {
		password := "Synthetic!" + random()[:32]
		hash, e := common.Password2Hash(password)
		must(e)
		b := make([]byte, 20)
		_, e = rand.Read(b)
		must(e)
		totp := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
		u := model.User{Id: s.ID, Username: "uci_" + s.Name, Password: hash, DisplayName: "Synthetic " + s.Name, Role: s.Role, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: "uci_aff_" + s.Name, Quota: 1000000, QuotaPaid: 1000000}
		must(db.Create(&u).Error)
		must(db.Create(&model.TwoFA{UserId: s.ID, Secret: totp, IsEnabled: true}).Error)
		accounts[s.Name] = map[string]any{"user_id": s.ID, "username": u.Username, "password": password, "totp_secret": totp}
	}
	keys := map[string]any{}
	for i, name := range []string{"old", "second", "zero_token", "disabled", "expired", "exhausted", "limited", "ip_limited", "bad_group", "other"} {
		tok := model.Token{Id: 4301 + i, UserId: 4201, Key: random(), Name: "synthetic-" + name, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, CreatedTime: time.Now().Unix()}
		switch name {
		case "disabled":
			tok.Status = common.TokenStatusDisabled
		case "expired":
			tok.ExpiredTime = time.Now().Unix() - 60
		case "exhausted":
			tok.Status = common.TokenStatusExhausted
		case "limited":
			tok.ModelLimitsEnabled = true
			tok.ModelLimits = "only-allowed-model"
		case "ip_limited":
			v := "192.0.2.0/24"
			tok.AllowIps = &v
		case "bad_group":
			tok.Group = "forbidden-fixture-group"
		case "other":
			tok.UserId = 4202
		case "zero_token":
			tok.UnlimitedQuota = false
		}
		must(db.Create(&tok).Error)
		keys[name] = map[string]any{"id": tok.Id, "key": "sk-" + tok.Key}
	}
	base := "http://127.0.0.1:35886"
	ch := model.Channel{Id: 4401, Type: 1, Key: "synthetic-upstream-no-real-credential", Name: "Synthetic loopback only", Status: 1, BaseURL: &base, Models: "gpt-4o-mini", Group: "default"}
	must(db.Create(&ch).Error)
	must(db.Create(&model.Ability{Group: "default", Model: "gpt-4o-mini", ChannelId: 4401, Enabled: true}).Error)
	must(db.Create(&model.Midjourney{UserId: 4201, MjId: "synthetic-intercept-mj", ImageUrl: base + "/image", Status: "SUCCESS", Progress: "100%", FinishTime: time.Now().Unix(), ChannelId: 4401}).Error)
	must(db.Create(&model.Task{TaskID: "synthetic-intercept-video", UserId: 4201, Status: model.TaskStatusSuccess, Action: "TEXT_GENERATE", ChannelId: 4401}).Error)
	access, e := service.IssueTaskArtifactAccess("synthetic-intercept-video", "video")
	must(e)
	for k, v := range map[string]string{"PasswordLoginEnabled": "true", "PasswordRegisterEnabled": "false"} {
		must(db.Exec("INSERT INTO options (key,value) VALUES (?,?) ON CONFLICT (key) DO UPDATE SET value=excluded.value", k, v).Error)
	}
	var n int64
	must(db.Model(&model.Setup{}).Count(&n).Error)
	if n == 0 {
		must(db.Create(&model.Setup{Version: "synthetic-call-interception", InitializedAt: time.Now().Unix()}).Error)
	}
	out := map[string]any{"base_url": "http://127.0.0.1:35881", "accounts": accounts, "keys": keys, "artifact_access": access, "synthetic_only": true}
	raw, e := common.Marshal(out)
	must(e)
	must(os.WriteFile(os.Args[1], raw, 0600))
	fmt.Println("Seeded isolated synthetic accounts, keys, loopback channel and artifact capabilities; no fixture credentials emitted.")
}
