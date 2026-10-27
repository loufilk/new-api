package model

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func testGroupNamespaceConcurrentWriters(t *testing.T) {
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	previousMax := sqlDB.Stats().MaxOpenConnections
	sqlDB.SetMaxOpenConns(max(previousMax, 4))
	t.Cleanup(func() {
		sqlDB.SetMaxOpenConns(previousMax)
	})

	type scenario struct {
		name string
		seed func(*testing.T) (func() error, func(*testing.T))
	}
	scenarios := []scenario{
		{
			name: "channel_insert",
			seed: func(t *testing.T) (func() error, func(*testing.T)) {
				stale := &Channel{Name: "stale-insert", Key: "sk-stale-insert", Status: common.ChannelStatusEnabled, Group: "old", Models: "m1"}
				return stale.Insert, func(t *testing.T) {
					var count int64
					require.NoError(t, DB.Model(&Channel{}).Where(commonGroupCol+" = ?", "old").Count(&count).Error)
					assert.Zero(t, count)
				}
			},
		},
		{
			name: "channel_update",
			seed: func(t *testing.T) (func() error, func(*testing.T)) {
				channel := Channel{Name: "stale-channel", Key: "sk-stale-channel", Status: common.ChannelStatusEnabled, Group: "old", Models: "m1"}
				require.NoError(t, DB.Create(&channel).Error)
				stale := channel
				stale.Name = "stale-write"
				return stale.Update, func(t *testing.T) {
					var stored Channel
					require.NoError(t, DB.First(&stored, channel.Id).Error)
					assert.Equal(t, "new", stored.Group)
				}
			},
		},
		{
			name: "token_group_and_auto_groups",
			seed: func(t *testing.T) (func() error, func(*testing.T)) {
				user := User{Username: "token-user", AffCode: "token-user", Status: common.UserStatusEnabled, Group: "old"}
				require.NoError(t, DB.Create(&user).Error)
				token := Token{UserId: user.Id, Key: "stale-token", Name: "stale", Status: common.TokenStatusEnabled, Group: "old", AutoGroups: `["old"]`}
				require.NoError(t, DB.Create(&token).Error)
				stale := token
				return stale.Update, func(t *testing.T) {
					var stored Token
					require.NoError(t, DB.First(&stored, token.Id).Error)
					assert.Equal(t, "new", stored.Group)
					assert.JSONEq(t, `["new"]`, stored.AutoGroups)
				}
			},
		},
		{
			name: "user_group",
			seed: func(t *testing.T) (func() error, func(*testing.T)) {
				user := User{Username: "stale-user", AffCode: "stale-user", Status: common.UserStatusEnabled, Group: "old"}
				require.NoError(t, DB.Create(&user).Error)
				stale := user
				return func() error { return stale.Edit(false) }, func(t *testing.T) {
					var stored User
					require.NoError(t, DB.First(&stored, user.Id).Error)
					assert.Equal(t, "new", stored.Group)
				}
			},
		},
		{
			name: "user_update_group",
			seed: func(t *testing.T) (func() error, func(*testing.T)) {
				user := User{Username: "stale-update-user", AffCode: "stale-update-user", Status: common.UserStatusEnabled, Group: "old"}
				require.NoError(t, DB.Create(&user).Error)
				stale := user
				return func() error { return stale.Update(false) }, func(t *testing.T) {
					var stored User
					require.NoError(t, DB.First(&stored, user.Id).Error)
					assert.Equal(t, "new", stored.Group)
				}
			},
		},
		{
			name: "subscription_plan",
			seed: func(t *testing.T) (func() error, func(*testing.T)) {
				plan := SubscriptionPlan{Title: "stale-plan", UpgradeGroup: "old", DowngradeGroup: "old", Enabled: true}
				require.NoError(t, DB.Create(&plan).Error)
				stale := plan
				return func() error { return DB.Save(&stale).Error }, func(t *testing.T) {
					var stored SubscriptionPlan
					require.NoError(t, DB.First(&stored, plan.Id).Error)
					assert.Equal(t, "new", stored.UpgradeGroup)
					assert.Equal(t, "new", stored.DowngradeGroup)
				}
			},
		},
		{
			name: "active_user_subscription",
			seed: func(t *testing.T) (func() error, func(*testing.T)) {
				user := User{Username: "sub-user", AffCode: "sub-user", Status: common.UserStatusEnabled, Group: "old"}
				require.NoError(t, DB.Create(&user).Error)
				plan := SubscriptionPlan{Title: "sub-plan", UpgradeGroup: "old", DowngradeGroup: "old", Enabled: true}
				require.NoError(t, DB.Create(&plan).Error)
				sub := UserSubscription{UserId: user.Id, PlanId: plan.Id, Status: "active", UpgradeGroup: "old", PrevUserGroup: "old", DowngradeGroup: "old"}
				require.NoError(t, DB.Create(&sub).Error)
				stale := sub
				return func() error { return DB.Save(&stale).Error }, func(t *testing.T) {
					var stored UserSubscription
					require.NoError(t, DB.First(&stored, sub.Id).Error)
					assert.Equal(t, "new", stored.UpgradeGroup)
					assert.Equal(t, "new", stored.PrevUserGroup)
					assert.Equal(t, "new", stored.DowngradeGroup)
				}
			},
		},
		{
			name: "ability",
			seed: func(t *testing.T) (func() error, func(*testing.T)) {
				channel := Channel{Name: "ability-channel", Key: "sk-ability", Status: common.ChannelStatusEnabled, Group: "old", Models: "m1"}
				require.NoError(t, DB.Create(&channel).Error)
				ability := Ability{Group: "old", Model: "m1", ChannelId: channel.Id, Enabled: true}
				require.NoError(t, DB.Create(&ability).Error)
				stale := ability
				return func() error { return DB.Save(&stale).Error }, func(t *testing.T) {
					var oldCount int64
					require.NoError(t, DB.Model(&Ability{}).
						Where("channel_id = ? AND model = ? AND "+commonGroupCol+" = ?", channel.Id, "m1", "old").
						Count(&oldCount).Error)
					assert.Zero(t, oldCount)
					var newCount int64
					require.NoError(t, DB.Model(&Ability{}).
						Where("channel_id = ? AND model = ? AND "+commonGroupCol+" = ?", channel.Id, "m1", "new").
						Count(&newCount).Error)
					assert.Equal(t, int64(1), newCount)
				}
			},
		},
	}

	for _, tc := range scenarios {
		t.Run(tc.name, func(t *testing.T) {
			setupGroupRenameTest(t)
			seedGroupRenameOption(t, "GroupRatio", `{"old":0.8}`)
			writer, verify := tc.seed(t)

			renameAtCommit := make(chan struct{})
			releaseRename := make(chan struct{})
			var hookOnce sync.Once
			previousHook := groupRenameTxFailureHook
			groupRenameTxFailureHook = func() error {
				hookOnce.Do(func() { close(renameAtCommit) })
				<-releaseRename
				return nil
			}
			t.Cleanup(func() { groupRenameTxFailureHook = previousHook })

			renameResult := make(chan error, 1)
			go func() {
				_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
				renameResult <- err
			}()
			<-renameAtCommit

			writerStarted := make(chan struct{})
			writerResult := make(chan error, 1)
			go func() {
				close(writerStarted)
				writerResult <- writer()
			}()
			<-writerStarted
			select {
			case err := <-writerResult:
				t.Fatalf("group writer completed while rename transaction still held namespace lock: %v", err)
			case <-time.After(100 * time.Millisecond):
				// The writer has entered its transaction while rename still
				// owns the namespace lock; it must remain blocked until release.
			}

			close(releaseRename)
			require.NoError(t, <-renameResult)
			writeErr := <-writerResult
			require.Error(t, writeErr)
			require.ErrorIs(t, writeErr, ErrGroupReferenceRenamed)
			verify(t)
		})
	}
}

func testGroupNamespaceRollbackState(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":0.8}`)
	previousHook := groupRenameTxFailureHook
	groupRenameTxFailureHook = func() error { return errors.New("forced namespace rollback") }
	t.Cleanup(func() { groupRenameTxFailureHook = previousHook })

	_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.Error(t, err)

	var count int64
	require.NoError(t, DB.Model(&Option{}).Where(map[string]any{"key": []string{groupRenameAliasesOptionKey, groupConfigVersionOptionKey}}).Count(&count).Error)
	assert.Zero(t, count, "rename alias/version state must roll back with the transaction")

	groupRenameTxFailureHook = nil
	channel := Channel{Name: "old-after-rollback", Key: "sk-old-after-rollback", Status: common.ChannelStatusEnabled, Group: "old", Models: "m1"}
	require.NoError(t, channel.Insert(), "rolled-back rename must not tombstone the old group")
}

func testGroupNamespaceSubscriptionPlanMapWriter(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":0.8}`)
	plan := SubscriptionPlan{Title: "plan", UpgradeGroup: "old", DowngradeGroup: "old", Enabled: true}
	require.NoError(t, DB.Create(&plan).Error)

	renameAtCommit := make(chan struct{})
	releaseRename := make(chan struct{})
	previousHook := groupRenameTxFailureHook
	groupRenameTxFailureHook = func() error {
		close(renameAtCommit)
		<-releaseRename
		return nil
	}
	t.Cleanup(func() { groupRenameTxFailureHook = previousHook })

	renameResult := make(chan error, 1)
	go func() {
		_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
		renameResult <- err
	}()
	<-renameAtCommit

	writerResult := make(chan error, 1)
	go func() {
		writerResult <- DB.Transaction(func(tx *gorm.DB) error {
			stale := SubscriptionPlan{Title: "stale", UpgradeGroup: "old", DowngradeGroup: "old"}
			old, new := "old", "old"
			return UpdateSubscriptionPlanTx(tx, plan.Id, SubscriptionPlanUpdate{
				Plan:           stale,
				UpgradeGroup:   &old,
				DowngradeGroup: &new,
			})
		})
	}()
	select {
	case err := <-writerResult:
		t.Fatalf("map writer completed while rename held namespace lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseRename)
	require.NoError(t, <-renameResult)
	writerErr := <-writerResult
	require.ErrorIs(t, writerErr, ErrGroupReferenceRenamed)

	var stored SubscriptionPlan
	require.NoError(t, DB.First(&stored, plan.Id).Error)
	assert.Equal(t, "new", stored.UpgradeGroup)
	assert.Equal(t, "new", stored.DowngradeGroup)
}

func testSubscriptionPlanLegacyGroupEdits(t *testing.T) {
	for _, tc := range []struct {
		name          string
		upgrade       *string
		downgrade     *string
		wantUpgrade   string
		wantDowngrade string
		wantError     bool
	}{
		{"omitted", nil, nil, "old", "old", false},
		{"clear_upgrade_preserve_downgrade", common.GetPointer(""), nil, "", "old", false},
		{"change_downgrade_preserve_upgrade", nil, common.GetPointer("new"), "old", "new", false},
		{"explicit_retired_rolls_back", common.GetPointer("new"), common.GetPointer("old"), "old", "old", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupGroupRenameTest(t)
			seedGroupRenameOption(t, "GroupRatio", `{"old":1}`)
			plan := SubscriptionPlan{Title: "original", UpgradeGroup: "old", DowngradeGroup: "old", Enabled: true, PriceAmount: 9}
			require.NoError(t, DB.Create(&plan).Error)
			require.NoError(t, DB.Model(&Option{}).Where(&Option{Key: "GroupRatio"}).Update("value", `{"new":1}`).Error)
			seedGroupRenameOption(t, "GroupRenameAliases", `{"old":"new"}`)
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"new":1}`))

			err := DB.Transaction(func(tx *gorm.DB) error {
				return UpdateSubscriptionPlanTx(tx, plan.Id, SubscriptionPlanUpdate{
					Plan:         SubscriptionPlan{Title: "edited", PriceAmount: 0, Enabled: false},
					UpgradeGroup: tc.upgrade, DowngradeGroup: tc.downgrade,
				})
			})
			if tc.wantError {
				require.ErrorIs(t, err, ErrGroupReferenceRenamed)
			} else {
				require.NoError(t, err)
			}
			var stored SubscriptionPlan
			require.NoError(t, DB.First(&stored, plan.Id).Error)
			assert.Equal(t, tc.wantUpgrade, stored.UpgradeGroup)
			assert.Equal(t, tc.wantDowngrade, stored.DowngradeGroup)
			if tc.wantError {
				assert.Equal(t, plan.Title, stored.Title)
				assert.Equal(t, plan.PriceAmount, stored.PriceAmount)
				assert.Equal(t, plan.Enabled, stored.Enabled)
			} else {
				assert.Equal(t, "edited", stored.Title)
				assert.Zero(t, stored.PriceAmount)
				assert.False(t, stored.Enabled)
			}
		})
	}
}

func testGroupConfigPubSubAndFallback(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":0.8}`)
	channel := Channel{Name: "pubsub-channel", Key: "sk-pubsub", Status: common.ChannelStatusEnabled, Group: "old", Models: "m1"}
	require.NoError(t, channel.Insert())

	_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.NoError(t, err)
	var versionOption Option
	require.NoError(t, DB.Where(&Option{Key: groupConfigVersionOptionKey}).First(&versionOption).Error)
	version, err := strconv.ParseInt(versionOption.Value, 10, 64)
	require.NoError(t, err)
	require.Positive(t, version)

	previousVersion := groupConfigAppliedVersion.Load()
	t.Cleanup(func() { groupConfigAppliedVersion.Store(previousVersion) })

	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"old":0.8}`))
	groupConfigAppliedVersion.Store(version - 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pubsub := common.RDB.Subscribe(ctx, groupConfigTopic(common.RDB.Options().DB))
	defer pubsub.Close()
	_, err = pubsub.Receive(ctx)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		receiveGroupConfigEvents(ctx, pubsub.Channel())
	}()
	require.NoError(t, PublishGroupConfigInvalidation(version, []GroupRenameMapping{{From: "old", To: "new"}}))
	require.Eventually(t, func() bool {
		return ratio_setting.ContainsGroupRatio("new") && !ratio_setting.ContainsGroupRatio("old") &&
			groupConfigAppliedVersion.Load() == version
	}, 5*time.Second, 10*time.Millisecond)

	cancel()
	<-done

	// Simulate a missed Pub/Sub message. The periodic option loader must recover
	// runtime state and dependent caches from the persisted version.
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"old":0.8}`))
	groupConfigAppliedVersion.Store(version - 1)
	loadOptionsFromDatabase()
	assert.True(t, ratio_setting.ContainsGroupRatio("new"))
	assert.False(t, ratio_setting.ContainsGroupRatio("old"))
	assert.Equal(t, version, groupConfigAppliedVersion.Load())
}

func TestGroupConfigSecondProcessHelper(t *testing.T) {
	mode := os.Getenv("TEST_GROUP_CONFIG_CHILD_MODE")
	if mode == "" {
		t.Skip("second-process helper")
	}
	require.Contains(t, []string{"subscriber", "fallback"}, mode)

	dsn := os.Getenv("TEST_GROUP_RENAME_POSTGRES_DSN")
	redisAddr := os.Getenv("TEST_REDIS_ADDR")
	prefix := os.Getenv("TEST_GROUP_CONFIG_CHILD_PREFIX")
	require.NotEmpty(t, dsn)
	require.NotEmpty(t, redisAddr)
	require.NotEmpty(t, prefix)

	common.IsMasterNode = false
	require.NoError(t, os.Setenv("SQL_DSN", dsn))
	require.NoError(t, InitDB())
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	require.NoError(t, rdb.Ping(context.Background()).Err())
	t.Cleanup(func() { _ = rdb.Close() })
	common.RedisEnabled, common.RDB = true, rdb
	common.MemoryCacheEnabled = true

	// Snapshot node B before node A renames anything.
	InitOptionMap()
	InitChannelCache()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var pubsub *redis.PubSub
	var subscriberDone chan struct{}
	if mode == "subscriber" {
		pubsub = rdb.Subscribe(ctx, groupConfigTopic(rdb.Options().DB))
		_, receiveErr := pubsub.Receive(ctx)
		require.NoError(t, receiveErr)
		subscriberDone = make(chan struct{})
		go func() {
			defer close(subscriberDone)
			receiveGroupConfigEvents(ctx, pubsub.Channel())
		}()
		t.Cleanup(func() {
			_ = pubsub.Close()
			cancel()
			<-subscriberDone
		})
	}

	readyKey := prefix + ":" + mode + ":ready"
	require.NoError(t, rdb.Set(ctx, readyKey, "1", 30*time.Second).Err())

	if mode == "fallback" {
		triggerKey := prefix + ":fallback:sync"
		require.Eventually(t, func() bool {
			value, err := rdb.Get(ctx, triggerKey).Result()
			return err == nil && value == "1"
		}, 10*time.Second, 10*time.Millisecond, "parent never triggered periodic-sync fallback")
		// Equivalent to one SyncOptions tick without sleeping for 60 seconds.
		loadOptionsFromDatabase()
	}

	require.Eventually(t, func() bool {
		if !ratio_setting.ContainsGroupRatio("new") || ratio_setting.ContainsGroupRatio("old") {
			return false
		}
		groups := setting.GetUserUsableGroupsCopy()
		if groups["new"] != "New group" {
			return false
		}
		if _, stale := groups["old"]; stale {
			return false
		}
		channel, err := GetRandomSatisfiedChannel("new", "m1", 0, nil)
		if err != nil || channel == nil || channel.Group != "new" {
			return false
		}
		oldChannel, err := GetRandomSatisfiedChannel("old", "m1", 0, nil)
		return err == nil && oldChannel == nil
	}, 10*time.Second, 10*time.Millisecond,
		"node B did not converge group ratio, permission map and channel routing to the renamed key")
}

type groupConfigChildProcess struct {
	mode   string
	cmd    *exec.Cmd
	output *bytes.Buffer
	done   chan error
}

func startGroupConfigChild(t *testing.T, mode, prefix string) *groupConfigChildProcess {
	t.Helper()
	buffer := &bytes.Buffer{}
	cmd := exec.Command(os.Args[0], "-test.run=^TestGroupConfigSecondProcessHelper$", "-test.v")
	cmd.Env = append(os.Environ(),
		"TEST_GROUP_CONFIG_CHILD_MODE="+mode,
		"TEST_GROUP_CONFIG_CHILD_PREFIX="+prefix,
	)
	cmd.Stdout = buffer
	cmd.Stderr = buffer
	require.NoError(t, cmd.Start())
	child := &groupConfigChildProcess{
		mode:   mode,
		cmd:    cmd,
		output: buffer,
		done:   make(chan error, 1),
	}
	go func() { child.done <- cmd.Wait() }()
	return child
}

func waitGroupConfigChild(t *testing.T, child *groupConfigChildProcess) {
	t.Helper()
	select {
	case err := <-child.done:
		require.NoErrorf(t, err, "second application process %s failed:\n%s", child.mode, child.output.String())
	case <-time.After(15 * time.Second):
		_ = child.cmd.Process.Kill()
		t.Fatalf("second application process %s timed out:\n%s", child.mode, child.output.String())
	}
}

func testGroupConfigTwoProcessInstances(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":0.8}`)
	seedGroupRenameOption(t, "UserUsableGroups", `{"old":"Old group"}`)
	channel := Channel{Name: "two-process-channel", Key: "sk-two-process", Status: common.ChannelStatusEnabled, Group: "old", Models: "m1"}
	require.NoError(t, channel.Insert())

	prefix := fmt.Sprintf("new-api:test:group-config:%d", os.Getpid())
	ctx := context.Background()
	for _, suffix := range []string{":subscriber:ready", ":fallback:ready", ":fallback:sync"} {
		_ = common.RDB.Del(ctx, prefix+suffix).Err()
	}
	t.Cleanup(func() {
		for _, suffix := range []string{":subscriber:ready", ":fallback:ready", ":fallback:sync"} {
			_ = common.RDB.Del(context.Background(), prefix+suffix).Err()
		}
	})

	subscriber := startGroupConfigChild(t, "subscriber", prefix)
	fallback := startGroupConfigChild(t, "fallback", prefix)

	for _, mode := range []string{"subscriber", "fallback"} {
		readyKey := prefix + ":" + mode + ":ready"
		require.Eventuallyf(t, func() bool {
			value, err := common.RDB.Get(ctx, readyKey).Result()
			return err == nil && value == "1"
		}, 10*time.Second, 10*time.Millisecond, "%s child never became ready", mode)
	}

	_, err := ApplyGroupRename(GroupRenameApplyRequest{
		Mappings: []GroupRenameMapping{{From: "old", To: "new"}},
		Options: map[string]string{
			"GroupRatio":       `{"new":0.8}`,
			"UserUsableGroups": `{"new":"New group"}`,
		},
		Expected: map[string]string{
			"GroupRatio":       `{"old":0.8}`,
			"UserUsableGroups": `{"old":"Old group"}`,
		},
	})
	require.NoError(t, err)

	// The fallback node deliberately did not subscribe. Trigger one periodic
	// option-sync iteration after rename; it must recover dependent caches from
	// the persisted version without waiting the normal 60-second interval.
	require.NoError(t, common.RDB.Set(ctx, prefix+":fallback:sync", "1", 30*time.Second).Err())

	waitGroupConfigChild(t, subscriber)
	waitGroupConfigChild(t, fallback)
}
