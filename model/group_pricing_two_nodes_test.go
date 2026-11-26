package model

import (
	"bufio"
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// This opt-in test runs two service processes against the same isolated DB,
// with Redis deliberately disabled. Node B keeps its original in-flight request
// alive while node A commits an ordinary group change and a personal override.
func TestGroupPricingCrossNodeReadAfterSave(t *testing.T) {
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires disposable loopback PostgreSQL 18.6/18.7")
	}
	t.Setenv(UserGroupPricingEnabledEnv, "true")
	ctx := context.Background()
	if firstID := os.Getenv("NEWAPI_TEST_GROUP_READER"); firstID != "" {
		first, err := strconv.Atoi(firstID)
		require.NoError(t, err)
		second, err := strconv.Atoi(os.Getenv("NEWAPI_TEST_GROUP_OTHER"))
		require.NoError(t, err)
		admitted, _ := gin.CreateTestContext(httptest.NewRecorder())
		admitted.Request = httptest.NewRequest("GET", "/local-test", nil)
		for scanner := bufio.NewScanner(os.Stdin); scanner.Scan(); {
			guard, err := BeginGroupConfigRead(ctx)
			require.NoError(t, err)
			frozen, err := GetRequestUserGroupPricing(admitted, first, "tier")
			require.NoError(t, err)
			current, err := LoadEffectiveUserGroupPricing(ctx, first)
			require.NoError(t, err)
			other, err := LoadEffectiveUserGroupPricing(ctx, second)
			require.NoError(t, err)
			f, c, o := frozen.ResolveGroupRatio("tier", "g"), current.ResolveGroupRatio("tier", "g"), other.ResolveGroupRatio("tier", "g")
			require.NoError(t, guard.Finish())
			fmt.Printf("TEST_QUOTE %.2f %.2f %.2f %d\n", f.GroupRatio, c.GroupRatio, o.GroupRatio, current.Revision)
		}
		return
	}
	setupGroupRenameTest(t)
	require.NoError(t, DB.AutoMigrate(&UserGroupPricing{}, &UserGroupPricingAudit{}))
	root := User{Username: "two-node-root", AffCode: "two-node-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	first := User{Username: "two-node-personal", AffCode: "two-node-personal", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "tier"}
	second := User{Username: "two-node-other", AffCode: "two-node-other", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "tier"}
	for _, u := range []*User{&root, &first, &second} {
		require.NoError(t, DB.Create(u).Error)
	}
	session := UserSession{SID: "two-node-root-session", UserID: root.Id, Version: 1, UserAuthVersion: 1, Status: UserSessionStatusActive, RefreshHash: strings.Repeat("a", 64), LoginMethod: "password", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, DB.Create(&session).Error)
	identity := AuthSessionIdentity{UserID: root.Id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1}
	t.Cleanup(func() {
		require.NoError(t, DB.Where("user_id = ?", first.Id).Delete(&UserGroupPricingAudit{}).Error)
		require.NoError(t, DB.Where("user_id = ?", first.Id).Delete(&UserGroupPricing{}).Error)
	})
	initial := `{"g":1.2}`
	seedGroupRenameOption(t, "GroupRatio", initial)
	// Warm B once; it has no subscription or cache invalidation from A.
	workerCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	nodeB := exec.CommandContext(workerCtx, os.Args[0], "-test.run=^TestGroupPricingCrossNodeReadAfterSave$", "-test.v")
	nodeB.Env = append(os.Environ(), fmt.Sprintf("NEWAPI_TEST_GROUP_READER=%d", first.Id), fmt.Sprintf("NEWAPI_TEST_GROUP_OTHER=%d", second.Id))
	stdin, err := nodeB.StdinPipe()
	require.NoError(t, err)
	stdout, err := nodeB.StdoutPipe()
	require.NoError(t, err)
	nodeB.Stderr = os.Stderr
	require.NoError(t, nodeB.Start())
	defer func() {
		_ = stdin.Close()
		require.NoError(t, nodeB.Wait(), "second node exited with an error")
	}()
	scan := bufio.NewScanner(stdout)
	quote := func(expected string) {
		t.Helper()
		_, err := fmt.Fprintln(stdin, "quote")
		require.NoError(t, err)
		for scan.Scan() {
			if strings.HasPrefix(scan.Text(), "TEST_QUOTE ") {
				require.Equal(t, "TEST_QUOTE "+expected, scan.Text())
				t.Logf("node B (admitted, fresh personal, fresh other, revision): %s", expected)
				return
			}
			t.Log(scan.Text())
		}
		t.Fatalf("reader ended before quote: %v", scan.Err())
	}
	quote("1.20 1.20 1.20 0")
	_, err = ApplyGroupRename(GroupRenameApplyRequest{Identity: &identity, ActorID: root.Id, ActorRole: common.RoleRootUser, Options: map[string]string{"GroupRatio": `{"g":2}`}, Expected: map[string]string{"GroupRatio": initial}})
	require.NoError(t, err)
	quote("1.20 2.00 2.00 0")
	rate := 0.5
	updated, err := SaveUserGroupPricing(ctx, identity, first.Id, UserGroupPricingUpdate{Group: "g", Ratio: &rate, RequestID: "local-group-override"})
	require.NoError(t, err)
	quote("1.20 0.50 2.00 1")
	_, err = SaveUserGroupPricing(ctx, identity, first.Id, UserGroupPricingUpdate{Group: "g", ExpectedRevision: updated.Revision, Ratio: nil, RequestID: "local-group-remove"})
	require.NoError(t, err)
	quote("1.20 2.00 2.00 2")
}
