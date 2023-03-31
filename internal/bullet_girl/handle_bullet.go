package bullet_girl

import (
	"bili_danmaku/internal/svc"
	entity "bili_danmaku/internal/types"
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/zeromicro/go-zero/core/logx"
	"io"
	"math/rand"
	"strings"
	"time"
)

var handler *BulletHandler

type BulletHandler struct {
	BulletChan chan []byte
}

func pushToBulletHandler(message []byte) {
	handler.BulletChan <- message
}

func HandleBullet(ctx context.Context, svcCtx *svc.ServiceContext) {
	handler = &BulletHandler{
		BulletChan: make(chan []byte, 1000),
	}

	var message []byte
	for {
		select {
		case <-ctx.Done():
			goto END
		case message = <-handler.BulletChan:
			handle(message, svcCtx)
		}
	}
END:
}

func handle(message []byte, svcCtx *svc.ServiceContext) {
	var err error

	// 一个正文可能包含多个数据包，需要逐个解析
	index := 0
	for index < len(message) {

		// 读出包长
		var length uint32
		if err = binary.Read(bytes.NewBuffer(message[index:index+headLengthOffset]), binary.BigEndian, &length); err != nil {
			logx.Errorf("解析包长度失败", err)
			return
		}

		// 读出正文协议版本
		var ver Version
		if err = binary.Read(bytes.NewBuffer(message[index+versionOffset:index+opcodeOffset]), binary.BigEndian, &ver); err != nil {
			logx.Errorf("解析正文协议版本失败", err)
			return
		}

		// 读出操作码
		var op Opcode
		if err = binary.Read(bytes.NewBuffer(message[index+opcodeOffset:index+magicOffset]), binary.BigEndian, &op); err != nil {
			logx.Errorf("解析操作码失败", err)
			return
		}

		// 读出正文内容
		body := message[index+packageLength : index+int(length)]

		// 解析正文内容
		switch ver {
		case normalJson:
			text := &entity.CmdText{}
			_ = json.Unmarshal(body, text)
			//logx.Infof("普通json包：%s,%v,%v", text.Cmd, ver, op)
			if op == command {
				switch Cmd(text.Cmd) {

				// 处理弹幕
				case DanmuMsg:
					danmu := &entity.DanmuMsgText{}
					_ = json.Unmarshal(body, danmu)
					from := danmu.Info[2].([]interface{})

					// 如果发现弹幕在@我，那么调用机器人进行回复
					y, content := checkIsAtMe(danmu.Info[1].(string), svcCtx)
					if y && danmu.Info[1].(string) != svcCtx.Config.EntryMsg {
						PushToBulletRobot(content)
					}

					logx.Infof("%.0f %v:%v", from[0].(float64), from[1], danmu.Info[1])

				// 进场特效欢迎
				case entryEffect:
					entry := &entity.EntryEffectText{}
					_ = json.Unmarshal(body, entry)
					if v, ok := svcCtx.Config.WelcomeString[fmt.Sprint(entry.Data.Uid)]; svcCtx.Config.WelcomeSwitch && ok && svcCtx.Config.EntryEffect {
						PushToBulletSender(v)
					} else if svcCtx.Config.EntryEffect {
						//PushToBulletSender(welcomeCaptain(entry.Data.CopyWriting))
						pushToInterractChan(&InterractData{
							Uid: entry.Data.Uid,
							Msg: welcomeCaptain(entry.Data.CopyWriting),
						})
					}

				// 欢迎进入房间（该功能会欢迎所有进入房间的人，可能会造成刷屏）
				case interactWord:
					interact := &entity.InteractWordText{}
					_ = json.Unmarshal(body, interact)
					// 1 进场 2 关注
					if interact.Data.MsgType == 1 {
						if v, ok := svcCtx.Config.WelcomeString[fmt.Sprint(interact.Data.Uid)]; svcCtx.Config.WelcomeSwitch && ok {
							PushToBulletSender(v)
						} else if svcCtx.Config.InteractWord {
							pushToInterractChan(&InterractData{
								Uid: interact.Data.Uid,
								Msg: handleInterract(welcomeInteract(interact.Data.Uname)),
							})
						}
					} else {
						msg := "感谢 " + interact.Data.Uname + " 的关注!"
						PushToBulletSender(msg)
						if svcCtx.Config.FocusDanmu != nil && len(svcCtx.Config.FocusDanmu) > 0 {
							rand.Seed(time.Now().UnixMicro())
							PushToBulletSender(svcCtx.Config.FocusDanmu[rand.Intn(len(svcCtx.Config.FocusDanmu))])
						}
					}

				// 感谢礼物
				case sendGift:
					if svcCtx.Config.ThanksGift {
						send := &entity.SendGiftText{}
						_ = json.Unmarshal(body, send)
						pushToGiftChan(send)
					}
				case "PK_BATTLE_START_NEW", "PK_BATTLE_START":
					if svcCtx.Config.PKNotice {
						info := &entity.PKStartInfo{}
						roomid := 0
						err := json.Unmarshal(body, info)
						if err != nil {
							logx.Error(err)
							return
						}
						if info.Data.InitInfo.RoomId == svcCtx.Config.RoomId {
							roomid = info.Data.MatchInfo.RoomId
						} else {
							roomid = info.Data.InitInfo.RoomId
						}
						logx.Debug("开始pk")
						//go handlerPK(svcCtx, body)
						pushToPKChan(&roomid)
					}
					//default:
					//	logx.Debug("---------------------")
					//	logx.Debug(text.Cmd)
					//	logx.Debug(string(body))
					//	logx.Debug("---------------------")
				}
			}
		case heartOrCertification:
			logx.Infof("心跳回复包")
		case normalZlib:
			b := bytes.NewReader(body)
			r, _ := zlib.NewReader(b)
			var out bytes.Buffer
			_, _ = io.Copy(&out, r)
			handle(out.Bytes(), svcCtx) // zlib解压后再进行格式解析
		}
		index += int(length)
	}
}

// 欢迎舰长语句
func welcomeCaptain(s string) string {
	s = strings.Replace(s, "\u003c%", "", 1)
	s = strings.Replace(s, "%\u003e", "", 1)

	return s
}

func welcomeInteract(name string) string {
	if strings.Contains(name, "欢迎") {
		name = strings.Replace(name, "欢迎", "", 1)
		return name
	} else {
		return name
	}
}

func handleInterract(uname string) string {
	s := []rune(uname)
	if len(s) > 13 {
		return "[欢迎 " + string(s[0:10]) + " ~]"
	} else {
		return "[欢迎 " + uname + " ~]"
	}
}
