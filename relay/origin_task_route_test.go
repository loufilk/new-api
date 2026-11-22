package relay

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPostgresNativeRemixResolvesAuthorizedOriginBeforeFirstHop(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL 18.6")
	}
	db := setupRelayChannelPostgresDB(t, dsn)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Ability{}))
	require.NoError(t, i18n.Init())
	prev := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(prev)) })
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"origin-group":2}`))
	ch := &model.Channel{Name: "authorized-origin", Key: "fixture-only", Type: constant.ChannelTypeDoubaoVideo, Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(ch).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "origin-group", Model: "fixture-video", ChannelId: ch.Id, Enabled: true}).Error)
	task := &model.Task{UserId: 41, TaskID: "local-origin", Group: "origin-group", ChannelId: ch.Id, Properties: model.Properties{OriginModelName: "fixture-video"}}
	require.NoError(t, db.Create(task).Error)
	for _, allowed := range []bool{true, false} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/local-origin/remix", nil)
		c.Params = gin.Params{{Key: "video_id", Value: task.TaskID}}
		common.SetContextKey(c, constant.ContextKeyUserId, task.UserId)
		common.SetContextKey(c, constant.ContextKeyUserGroup, task.Group)
		common.SetContextKey(c, constant.ContextKeyTokenGroup, task.Group)
		common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
		common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"fixture-video": allowed})
		info, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
		require.NoError(t, err)
		require.Nil(t, info.ChannelMeta)
		taskErr := ResolveOriginTask(c, info)
		if !allowed {
			require.NotNil(t, taskErr)
			require.Equal(t, http.StatusForbidden, taskErr.StatusCode)
			require.Nil(t, info.LockedChannel)
			require.Empty(t, common.GetContextKeyString(c, constant.ContextKeyChannelKey))
			continue
		}
		require.Nil(t, taskErr)
		require.NotNil(t, info.ChannelMeta)
		require.Equal(t, ch.Id, info.ChannelId)
		require.Equal(t, task.Group, info.UsingGroup)
		require.Equal(t, task.Group, common.GetContextKeyString(c, constant.ContextKeyAutoGroup))
		require.Equal(t, "fixture-video", info.OriginModelName)
	}
}
