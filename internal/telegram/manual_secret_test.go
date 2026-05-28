package telegram

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	gotd "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/config"
	"github.com/nemo/Tsumugi/internal/storage"
)

const (
	nemoManualGroupTitle       = "Nemo"
	nemoManualChannelTitleZHCN = "Nemo 的语句收集器"
	nemoManualChannelTitleZHTW = "Nemo 的語句收集器"
)

func TestManualSecretDialogs(t *testing.T) {
	if os.Getenv("TSUMUGI_MANUAL_SECRET") != "1" {
		t.Skip("set TSUMUGI_MANUAL_SECRET=1 to run the local Telegram smoke test")
	}
	flags, err := flagsFromSecret(filepath.Join("..", "..", "nemo.secret"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(flags)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	resolver, err := cfg.Proxy.Resolver()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	opts := gotd.Options{
		SessionStorage: &gotd.FileSessionStorage{Path: cfg.SessionPath(cfg.Phone)},
		Device:         deviceConfig(),
		UpdateHandler:  tg.NewUpdateDispatcher(),
	}
	if resolver != nil {
		opts.Resolver = resolver
	}
	client := gotd.NewClient(cfg.APIID, cfg.APIHash, opts)
	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if !status.Authorized {
			t.Skip("existing session is not authorized; complete interactive login in the TUI first")
		}
		savedPeer, nemoGroupPeer, nemoChannelPeer, nemoPrivatePeer, err := findManualPeers(ctx, client.API(), accountIDFromSelf(status.User))
		if err != nil {
			return err
		}
		if savedPeer == nil {
			t.Fatal("Saved Messages dialog was not found")
		}
		if nemoGroupPeer == nil {
			t.Log(`group dialog titled "` + nemoManualGroupTitle + `" was not found in searched dialogs`)
		}
		if nemoChannelPeer == nil {
			t.Log(`channel "` + nemoManualChannelTitleZHCN + `" was not found in searched dialogs`)
		}
		if nemoPrivatePeer == nil {
			t.Log(`private chat with user "` + nemoManualGroupTitle + `" (recent ping) was not found`)
		}
		if err := verifyManualFolders(ctx, client.API()); err != nil {
			return err
		}
		if os.Getenv("TSUMUGI_MANUAL_SEND") == "1" {
			if err := sendManualProbe(ctx, client.API(), savedPeer, "Tsumugi Saved Messages smoke test"); err != nil {
				return err
			}
			if nemoGroupPeer != nil {
				if err := sendManualProbe(ctx, client.API(), nemoGroupPeer, "Tsumugi Nemo group smoke test"); err != nil {
					return err
				}
			}
			if nemoChannelPeer != nil {
				if err := sendManualProbe(ctx, client.API(), nemoChannelPeer, "Tsumugi Nemo channel smoke test"); err != nil {
					return err
				}
			}
			if nemoPrivatePeer != nil {
				if err := sendManualProbe(ctx, client.API(), nemoPrivatePeer, "Tsumugi Nemo private chat smoke test"); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		t.Fatal(err)
	}
}

func verifyManualFolders(ctx context.Context, api *tg.Client) error {
	filters, err := api.MessagesGetDialogFilters(ctx)
	if err != nil {
		return err
	}
	_ = filters
	request := &tg.MessagesGetDialogsRequest{OffsetPeer: &tg.InputPeerEmpty{}, Limit: 10}
	request.SetFolderID(1)
	_, err = api.MessagesGetDialogs(ctx, request)
	return err
}

func isMegagroupChannel(p storage.Peer, e entitiesByID) bool {
	if p.Kind != "channel" {
		return false
	}
	ch := e.channels[p.ID]
	return ch != nil && ch.Megagroup
}

func isBroadcastChannelPeer(p storage.Peer, e entitiesByID) bool {
	if p.Kind != "channel" {
		return false
	}
	ch := e.channels[p.ID]
	return ch != nil && !ch.Megagroup
}

func isManualNemoGroup(peer storage.Peer, entities entitiesByID, title string) bool {
	if !strings.EqualFold(strings.TrimSpace(title), nemoManualGroupTitle) {
		return false
	}
	return peer.Kind == "chat" || isMegagroupChannel(peer, entities)
}

func isManualNemoChannel(peer storage.Peer, entities entitiesByID, title string) bool {
	if !isBroadcastChannelPeer(peer, entities) {
		return false
	}
	t := strings.TrimSpace(title)
	if t == nemoManualChannelTitleZHCN || t == nemoManualChannelTitleZHTW {
		return true
	}
	return strings.Contains(t, "语句收集器") || strings.Contains(t, "語句收集器")
}

func isManualNemoPrivate(peer storage.Peer, entities entitiesByID, title string) bool {
	if peer.Title == "Saved Messages" || peer.Kind != "user" {
		return false
	}
	u := entities.users[peer.ID]
	if u != nil && u.Bot {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(title), nemoManualGroupTitle)
}

func scorePrivateNemo(lastText string, lt string) int {
	score := 0
	if strings.TrimSpace(lastText) == "ping" {
		score += 100
	}
	if lt == "nemo" {
		score += 80
	}
	if strings.Contains(lt, "maa") || strings.Contains(lt, "notification bot") {
		score -= 40
	}
	return score
}

func findManualPeers(ctx context.Context, api *tg.Client, accountID string) (tg.InputPeerClass, tg.InputPeerClass, tg.InputPeerClass, tg.InputPeerClass, error) {
	var savedPeer tg.InputPeerClass
	var nemoGroupPeer, nemoChannelPeer, nemoPrivatePeer tg.InputPeerClass
	groupBest := -1
	channelBest := -1
	privateBest := -1

	offsetPeer := tg.InputPeerClass(&tg.InputPeerEmpty{})
	offsetDate := 0
	offsetID := 0
	for page := 0; page < 10; page++ {
		dialogs, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetPeer: offsetPeer,
			OffsetDate: offsetDate,
			OffsetID:   offsetID,
			Limit:      100,
		})
		if err != nil {
			return nil, nil, nil, nil, err
		}
		modified, ok := dialogs.AsModified()
		if !ok || len(modified.GetDialogs()) == 0 {
			break
		}
		entities := dialogEntities(modified.GetUsers(), modified.GetChats())
		messageByID := make(map[int]*tg.Message)
		for _, item := range modified.GetMessages() {
			if msg, ok := item.(*tg.Message); ok {
				messageByID[msg.ID] = msg
			}
		}
		for _, dialog := range modified.GetDialogs() {
			d, ok := dialog.(*tg.Dialog)
			if !ok {
				continue
			}
			peer, ok := peerFromRef(accountID, d.Peer, entities)
			if !ok {
				continue
			}
			input, err := inputPeer(peer)
			if err != nil {
				continue
			}
			title := strings.TrimSpace(peer.Title)
			if peer.Title == "Saved Messages" {
				savedPeer = input
			}
			lastText := ""
			if last := messageByID[d.TopMessage]; last != nil {
				lastText = strings.TrimSpace(last.Message)
			}
			lt := strings.ToLower(title)

			if isManualNemoGroup(peer, entities, title) {
				score := 10
				if lastText != "" {
					score++
				}
				if score > groupBest {
					groupBest = score
					nemoGroupPeer = input
				}
			}
			if isManualNemoChannel(peer, entities, title) {
				score := 10
				if score > channelBest {
					channelBest = score
					nemoChannelPeer = input
				}
			}
			if isManualNemoPrivate(peer, entities, title) {
				score := scorePrivateNemo(lastText, lt)
				if score > privateBest {
					privateBest = score
					nemoPrivatePeer = input
				}
			}
			if last := messageByID[d.TopMessage]; last != nil {
				offsetDate = last.Date
			}
			offsetID = d.TopMessage
			offsetPeer = input
		}
	}

	tryContactsSearch := nemoGroupPeer == nil || nemoChannelPeer == nil || nemoPrivatePeer == nil
	if tryContactsSearch {
		found, err := api.ContactsSearch(ctx, &tg.ContactsSearchRequest{Q: "Nemo", Limit: 20})
		if err == nil {
			entities := dialogEntities(found.Users, found.Chats)
			for _, ref := range append(found.MyResults, found.Results...) {
				peer, ok := peerFromRef(accountID, ref, entities)
				if !ok || !strings.Contains(strings.ToLower(peer.Title), "nemo") {
					continue
				}
				input, err := inputPeer(peer)
				if err != nil {
					continue
				}
				title := strings.TrimSpace(peer.Title)
				lt := strings.ToLower(title)

				if nemoGroupPeer == nil && isManualNemoGroup(peer, entities, title) {
					score := 8
					if score > groupBest {
						groupBest = score
						nemoGroupPeer = input
					}
				}
				if nemoChannelPeer == nil && isManualNemoChannel(peer, entities, title) {
					score := 8
					if score > channelBest {
						channelBest = score
						nemoChannelPeer = input
					}
				}
				if nemoPrivatePeer == nil && isManualNemoPrivate(peer, entities, title) {
					score := scorePrivateNemo("", lt)
					if score > privateBest {
						privateBest = score
						nemoPrivatePeer = input
					}
				}
			}
		}
	}
	return savedPeer, nemoGroupPeer, nemoChannelPeer, nemoPrivatePeer, nil
}

func sendManualProbe(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, text string) error {
	randomID, err := randomInt64()
	if err != nil {
		return err
	}
	_, err = api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  text,
		RandomID: randomID,
	})
	return err
}

func flagsFromSecret(path string) (config.CLIFlags, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return config.CLIFlags{}, err
	}
	fields := strings.Fields(string(raw))
	var flags config.CLIFlags
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "--proxy":
			i++
			flags.Proxy = fields[i]
		case "--login":
			i++
			flags.AuthMode = fields[i]
		case "--api-id":
			i++
			flags.APIID, _ = strconv.Atoi(fields[i])
		case "--api-hash":
			i++
			flags.APIHash = fields[i]
		case "--phone":
			i++
			flags.Phone = fields[i]
		}
	}
	return flags, nil
}
