package gitea

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	giteaStruct "code.gitea.io/gitea/modules/structs"
	"code.gitea.io/sdk/gitea"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitea/test"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestOkToTestComment(t *testing.T) {
	issueCommentPayload := &giteaStruct.IssueCommentPayload{
		Issue: &giteaStruct.Issue{
			URL: "http://url.com/owner/repo/1",
		},
	}
	tests := []struct {
		name          string
		commentsReply string
		runevent      info.Event
		allowed       bool
		wantErr       bool
	}{
		{
			name:          "allowed_from_org/good issue comment event",
			commentsReply: `[{"body": "/ok-to-test", "user": {"login": "owner"}}]`,
			runevent: info.Event{
				Organization: "owner",
				Repository:   "repo",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event:        issueCommentPayload,
			},
			allowed: true,
			wantErr: false,
		},
		{
			name:          "allowed_from_org/good issue pull request event",
			commentsReply: `[{"body": "/ok-to-test", "user": {"login": "owner"}}]`,
			runevent: info.Event{
				Organization: "owner",
				Repository:   "repo",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event:        issueCommentPayload,
			},
			allowed: true,
			wantErr: false,
		},
		{
			name:          "disallowed/bad event origin",
			commentsReply: `[{"body": "/ok-to-test", "user": {"login": "owner"}}]`,
			runevent: info.Event{
				Organization: "owner",
				Repository:   "repo",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event:        &giteaStruct.RepositoryPayload{},
			},
			allowed: false,
		},
		{
			name:          "disallowed/no-ok-to-test",
			commentsReply: `[{"body": "Foo Bar", "user": {"login": "owner"}}]`,
			runevent: info.Event{
				Organization: "owner",
				Repository:   "repo",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event:        issueCommentPayload,
			},
			allowed: false,
			wantErr: false,
		},
		{
			name:          "disallowed/ok-to-test-not-from-owner",
			commentsReply: `[{"body": "/ok-to-test", "user": {"login": "notowner"}}]`,
			runevent: info.Event{
				Organization: "owner",
				Repository:   "repo",
				Sender:       "nonowner",
				EventType:    "issue_comment",
				Event:        issueCommentPayload,
			},
			allowed: false,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.runevent.TriggerTarget = "ok-to-test-comment"
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()
			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues/1/comments", tt.runevent.Organization,
				tt.runevent.Repository),
				func(rw http.ResponseWriter,
					r *http.Request,
				) {
					fmt.Fprint(rw, tt.commentsReply)
				})
			mux.HandleFunc("/repos/owner/collaborators", func(rw http.ResponseWriter, r *http.Request) {
				fmt.Fprint(rw, "[]")
			})
			ctx, _ := rtesting.SetupFakeContext(t)
			gprovider := Provider{
				Client: fakeclient,
			}

			isAllowed, err := gprovider.IsAllowed(ctx, &tt.runevent)
			if tt.wantErr {
				assert.Assert(t, err != nil)
			} else {
				assert.Assert(t, err == nil)
			}
			assert.Assert(t, isAllowed == tt.allowed)
		})
	}
}

func TestAclCheckAll(t *testing.T) {
	type allowedRules struct {
		ownerFile bool
		collabo   bool
	}
	tests := []struct {
		name         string
		runevent     info.Event
		wantErr      bool
		allowedRules allowedRules
		allowed      bool
	}{
		{
			name: "allowed_from_org/sender allowed_from_org in collabo",
			runevent: info.Event{
				Organization: "collabo",
				Repository:   "repo",
				Sender:       "login_allowed",
			},
			allowedRules: allowedRules{collabo: true},
			allowed:      true,
			wantErr:      false,
		},
		{
			name: "allowed_from_org/sender allowed_from_org from owner file",
			runevent: info.Event{
				Organization:  "collabo",
				Repository:    "repo",
				Sender:        "approved_from_owner_file",
				DefaultBranch: "maine",
				BaseBranch:    "maine",
			},
			allowedRules: allowedRules{ownerFile: true},
			allowed:      true,
			wantErr:      false,
		},
		{
			name: "disallowed/sender not allowed_from_org in collabo",
			runevent: info.Event{
				Organization: "denied",
				Repository:   "denied",
				Sender:       "notallowed",
			},
			allowed: false,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()

			ctx, _ := rtesting.SetupFakeContext(t)
			gprovider := Provider{
				Client: fakeclient,
			}

			if tt.allowedRules.collabo {
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/collaborators/%s", tt.runevent.Organization,
					tt.runevent.Repository, tt.runevent.Sender), func(rw http.ResponseWriter, r *http.Request) {
					rw.WriteHeader(http.StatusNoContent)
				})
			}
			if tt.allowedRules.ownerFile {
				url := fmt.Sprintf("/repos/%s/%s/contents/OWNERS", tt.runevent.Organization, tt.runevent.Repository)
				mux.HandleFunc(url, func(rw http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("ref") != tt.runevent.DefaultBranch {
						rw.WriteHeader(http.StatusNotFound)
						return
					}
					encoded := base64.StdEncoding.EncodeToString([]byte(
						fmt.Sprintf("approvers:\n  - %s\n", tt.runevent.Sender)))
					// encode to json
					b, err := json.Marshal(gitea.ContentsResponse{
						Content: &encoded,
					})
					if err != nil {
						rw.WriteHeader(http.StatusInternalServerError)
						return
					}
					rw.WriteHeader(http.StatusOK)
					_, _ = rw.Write(b)
				})
			}
			isAllowed, err := gprovider.IsAllowed(ctx, &tt.runevent)
			if tt.wantErr {
				assert.Assert(t, err != nil)
			} else {
				assert.Assert(t, err == nil)
			}
			assert.Assert(t, isAllowed == tt.allowed)
		})
	}
}
