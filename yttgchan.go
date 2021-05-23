/*
go get -u -v github.com/kkdai/youtube/v2

https://github.com/kkdai/youtube
https://developers.google.com/youtube/v3/docs/playlistItems/list
https://core.telegram.org/bots/api

GoFmt GoBuildNull GoPublish

heroku git:clone -a gurusergeybugaev $HOME/gurusergeybugaev/
heroku buildpacks:set https://github.com/ryandotsmith/null-buildpack.git
GOOS=linux GOARCH=amd64 go build -trimpath -o $home/gurusergeybugaev/ && cp yttgchan.go $home/gurusergeybugaev/
cd $home/gurusergeybugaev/ && git commit -am yttgchan && git reset `{git commit-tree 'HEAD^{tree}' -m 'yttgchan+ffmpeg'} && git push -f
*/

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	dotenv "github.com/joho/godotenv"
	yt "github.com/kkdai/youtube/v2"
)

func log(msg string, args ...interface{}) {
	const Beat = time.Duration(24) * time.Hour / 1000
	tzBiel := time.FixedZone("Biel", 60*60)
	t := time.Now().In(tzBiel)
	ty := t.Sub(time.Date(t.Year(), 1, 1, 0, 0, 0, 0, tzBiel))
	td := t.Sub(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, tzBiel))
	ts := fmt.Sprintf(
		"%d/%d@%d",
		t.Year()%1000,
		int(ty/(time.Duration(24)*time.Hour))+1,
		int(td/Beat),
	)
	fmt.Fprintf(os.Stderr, ts+" "+msg+"\n", args...)
}

const (
	YtMaxResults = 50

	DotenvPath = "yttgchan.env"
)

var (
	Ctx        context.Context
	HttpClient = &http.Client{}
	YtCl       yt.Client

	YtKey        string
	YtUsername   string
	YtChannelId  string
	YtPlaylistId string
	YtLast       string

	TgToken        string
	TgChatId       string
	TgAudioBitrate string
	TgPerformer    string
	TgTitleCleanRe string
	TgTitleUnquote bool

	FfmpegPath string = "./ffmpeg"

	HerokuToken   string
	HerokuVarsUrl string
)

type YtChannel struct {
	Id             string `json:"id"`
	ContentDetails struct {
		RelatedPlaylists struct {
			Uploads string `json:"uploads"`
		} `json:"relatedPlaylists"`
	} `json:"contentDetails"`
}

type YtChannelListResponse struct {
	PageInfo struct {
		TotalResults   int64 `json:"totalResults"`
		ResultsPerPage int64 `json:"resultsPerPage"`
	} `json:"pageInfo"`
	Items []YtChannel `json:"items"`
}

type YtPlaylistItemSnippet struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	PublishedAt string `json:"publishedAt"`
	Thumbnails  struct {
		Medium struct {
			Url string `json:"url"`
		} `json:"medium"`
		High struct {
			Url string `json:"url"`
		} `json:"high"`
		Standard struct {
			Url string `json:"url"`
		} `json:"standard"`
		MaxRes struct {
			Url string `json:"url"`
		} `json:"maxres"`
	} `json:"thumbnails"`
	Position   int64 `json:"position"`
	ResourceId struct {
		VideoId string `json:"videoId"`
	} `json:"resourceId"`
}

type YtPlaylistItem struct {
	Snippet YtPlaylistItemSnippet `json:"snippet"`
}

type YtPlaylistItems struct {
	NextPageToken string `json:"nextPageToken"`
	PageInfo      struct {
		TotalResults   int64 `json:"totalResults"`
		ResultsPerPage int64 `json:"resultsPerPage"`
	} `json:"pageInfo"`
	Items []YtPlaylistItem
}

type TgResponse struct {
	Ok          bool       `json:"ok"`
	Description string     `json:"description"`
	Result      *TgMessage `json:"result"`
}

type TgResponseShort struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description"`
}

type TgPhotoSize struct {
	FileId       string `json:"file_id"`
	FileUniqueId string `json:"file_unique_id"`
	Width        int64  `json:"width"`
	Height       int64  `json:"height"`
	FileSize     int64  `json:"file_size"`
}

type TgAudio struct {
	FileId       string      `json:"file_id"`
	FileUniqueId string      `json:"file_unique_id"`
	Duration     int64       `json:"duration"`
	Performer    string      `json:"performer"`
	Title        string      `json:"title"`
	MimeType     string      `json:"mime_type"`
	FileSize     int64       `json:"file_size"`
	Thumb        TgPhotoSize `json:"thumb"`
}

type TgMessage struct {
	Id        string
	MessageId int64         `json:"message_id"`
	Audio     TgAudio       `json:"audio"`
	Photo     []TgPhotoSize `json:"photo"`
}

func getJson(url string, target interface{}) error {
	r, err := HttpClient.Get(url)
	if err != nil {
		return err
	}
	defer r.Body.Close()

	return json.NewDecoder(r.Body).Decode(target)
}

func postJson(url string, data *bytes.Buffer, target interface{}) error {
	resp, err := HttpClient.Post(
		url,
		"application/json",
		data,
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody := bytes.NewBuffer(nil)
	_, err = io.Copy(respBody, resp.Body)
	if err != nil {
		return fmt.Errorf("io.Copy: %v", err)
	}

	err = json.NewDecoder(respBody).Decode(target)
	if err != nil {
		return fmt.Errorf("Decode: %v", err)
	}

	return nil
}

func downloadFile(url string) (*bytes.Buffer, error) {
	resp, err := HttpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var bb = bytes.NewBuffer(nil)

	_, err = io.Copy(bb, resp.Body)
	if err != nil {
		return nil, err
	}

	return bb, nil
}

func Setenv(name, value string) error {
	if HerokuVarsUrl != "" && HerokuToken != "" {
		return HerokuSetenv(name, value)
	}

	env, err := dotenv.Read(DotenvPath)
	if err != nil {
		log("WARNING: loading dotenv file: %v", err)
		env = make(map[string]string)
	}
	env[name] = value
	if err = dotenv.Write(env, DotenvPath); err != nil {
		return err
	}

	return nil
}

func HerokuSetenv(name, value string) error {
	req, err := http.NewRequest(
		"PATCH",
		HerokuVarsUrl,
		strings.NewReader(fmt.Sprintf(`{"%s": "%s"}`, name, value)),
	)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "application/vnd.heroku+json; version=3")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", HerokuToken))
	req.Header.Set("Content-Type", "application/json")
	resp, err := HttpClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("response status: %s", resp.Status)
	}

	return nil
}

func init() {
	var err error

	Ctx = context.TODO()
	YtCl = yt.Client{HTTPClient: &http.Client{}}

	if err = dotenv.Overload(DotenvPath); err != nil {
		log("WARNING: loading dotenv file: %v", err)
	}

	if os.Getenv("TgToken") != "" {
		TgToken = os.Getenv("TgToken")
	}
	if TgToken == "" {
		log("ERROR: TgToken empty")
		os.Exit(1)
	}
	if os.Getenv("TgChatId") != "" {
		TgChatId = os.Getenv("TgChatId")
	}
	if TgChatId == "" {
		log("ERROR: TgChatId empty")
		os.Exit(1)
	}

	if os.Getenv("TgPerformer") != "" {
		TgPerformer = os.Getenv("TgPerformer")
	}
	if os.Getenv("TgAudioBitrate") != "" {
		TgAudioBitrate = os.Getenv("TgAudioBitrate")
	}
	if os.Getenv("TgTitleCleanRe") != "" {
		TgTitleCleanRe = os.Getenv("TgTitleCleanRe")
	}
	if os.Getenv("TgTitleUnquote") != "" {
		TgTitleUnquote = true
	}

	if os.Getenv("YtKey") != "" {
		YtKey = os.Getenv("YtKey")
	}
	if YtKey == "" {
		log("ERROR: YtKey empty")
		os.Exit(1)
	}
	if os.Getenv("YtUsername") != "" {
		YtUsername = os.Getenv("YtUsername")
	}
	if os.Getenv("YtChannelId") != "" {
		YtChannelId = os.Getenv("YtChannelId")
	}
	if os.Getenv("YtPlaylistId") != "" {
		YtPlaylistId = os.Getenv("YtPlaylistId")
	}
	if os.Getenv("YtLast") != "" {
		YtLast = os.Getenv("YtLast")
	}

	if os.Getenv("FfmpegPath") != "" {
		FfmpegPath = os.Getenv("FfmpegPath")
	}

	HerokuToken = os.Getenv("HerokuToken")
	if HerokuToken == "" {
		log("WARNING: HerokuToken empty")
	}
	HerokuVarsUrl = os.Getenv("HerokuVarsUrl")
	if HerokuVarsUrl == "" {
		log("WARNING: HerokuVarsUrl empty")
	}
}

func main() {
	var err error

	if YtPlaylistId == "" {
		if YtUsername == "" && YtChannelId == "" {
			log("Empty YtPlaylistId and YtUsername and YtChannelId, nothing to do")
			return
		}

		ChannelListUrl, err := url.Parse("https://www.googleapis.com/youtube/v3/channels")
		if err != nil {
			log("url.Parse: %v", err)
			return
		}
		ChannelListUrlValues := url.Values{}
		ChannelListUrlValues.Set("key", YtKey)
		ChannelListUrlValues.Set("part", "contentDetails")
		if YtUsername != "" {
			ChannelListUrlValues.Set("forUsername", YtUsername)
		} else if YtChannelId != "" {
			ChannelListUrlValues.Set("id", YtChannelId)
		}
		ChannelListUrl.RawQuery = ChannelListUrlValues.Encode()
		var userChannels YtChannelListResponse
		err = getJson(ChannelListUrl.String(), &userChannels)
		if err != nil {
			log("Failed to get channels list: %v", err)
			return
		}

		if len(userChannels.Items) == 0 {
			log("Empty channels list")
			return
		}
		YtPlaylistId = userChannels.Items[0].ContentDetails.RelatedPlaylists.Uploads
		if YtPlaylistId == "" {
			log("Empty playlist id was retrieved")
			return
		}
	}

	var videos []YtPlaylistItemSnippet

	for _, plid := range strings.Split(YtPlaylistId, " ") {
		if plid == "" {
			continue
		}
		nextPageToken := ""
		for nextPageToken != "" || len(videos) == 0 {
			var PlaylistItemsUrl = fmt.Sprintf("https://www.googleapis.com/youtube/v3/playlistItems?maxResults=%d&part=snippet&playlistId=%s&key=%s&pageToken=%s", YtMaxResults, plid, YtKey, nextPageToken)

			var playlistItems YtPlaylistItems
			err = getJson(PlaylistItemsUrl, &playlistItems)
			if err != nil {
				log("Failed to get playlist items: %v", err)
				return
			}

			if playlistItems.NextPageToken != nextPageToken {
				nextPageToken = playlistItems.NextPageToken
			} else {
				nextPageToken = ""
			}

			for _, i := range playlistItems.Items {
				videos = append(videos, i.Snippet)
			}
		}
	}

	log("Videos: %d", len(videos))

	sort.Slice(videos, func(i, j int) bool { return videos[i].PublishedAt < videos[j].PublishedAt })

	for vidnum, vid := range videos {
		var ytid, publishedAt string
		var title, description string
		var audioName string

		ytid = vid.ResourceId.VideoId

		publishedAt = strings.NewReplacer("-", "", "T", ".", ":", "").Replace(vid.PublishedAt)
		publishedAt = strings.TrimSuffix(publishedAt, "Z")
		publishedAt = strings.TrimSuffix(publishedAt, ".000")

		title = vid.Title
		if TgTitleCleanRe != "" {
			title = regexp.MustCompile(TgTitleCleanRe).ReplaceAllString(title, "")
		}
		if TgTitleUnquote {
			if strings.HasPrefix(title, `"`) && strings.HasSuffix(title, `"`) {
				title = strings.Trim(title, `"`)
			}
			if strings.HasPrefix(title, `«`) && strings.HasSuffix(title, `»`) {
				title = strings.Trim(title, `«`)
				title = strings.Trim(title, `»`)
			}
			for strings.Contains(title, `"`) {
				title = strings.Replace(title, `"`, `«`, 1)
				title = strings.Replace(title, `"`, `»`, 1)
			}
		}

		description = vid.Description

		audioName = fmt.Sprintf("%s.%s", publishedAt, ytid)

		if audioName == YtLast {
			log("Last: %s: #%d %s", YtLast, vidnum+1, title)
		}

		if audioName <= YtLast {
			continue
		}

		log("New: #%d %s: %s", vidnum+1, audioName, title)
		log("Description: %d letters", len([]rune(description)))

		var coverUrl, thumbUrl string
		var coverBuf, thumbBuf, audioBuf *bytes.Buffer

		coverUrl = vid.Thumbnails.MaxRes.Url
		if coverUrl == "" {
			coverUrl = vid.Thumbnails.Standard.Url
		}
		if coverUrl == "" {
			coverUrl = vid.Thumbnails.High.Url
		}
		if coverUrl == "" {
			coverUrl = vid.Thumbnails.Medium.Url
		}
		if coverUrl == "" {
			log("No cover url")
			break
		}

		thumbUrl = vid.Thumbnails.Medium.Url
		if thumbUrl == "" {
			log("No thumb url")
			break
		}

		coverBuf, err = downloadFile(coverUrl)
		if err != nil {
			log("Download cover: %v", err)
			break
		}
		log(
			"Cover: %dkb",
			coverBuf.Len()/1000,
		)

		thumbBuf, err = downloadFile(thumbUrl)
		if err != nil {
			log("Download thumb: %v", err)
			break
		}
		log(
			"Thumb: %dkb",
			thumbBuf.Len()/1000,
		)

		vinfo, err := YtCl.GetVideoContext(Ctx, ytid)
		if err != nil {
			log("GetVideoContext: %v", err)
			break
		}

		var audioFormat yt.Format
		for _, f := range vinfo.Formats {
			if !strings.HasPrefix(f.MimeType, "audio/mp4") {
				continue
			}
			if audioFormat.Bitrate == 0 || f.Bitrate < audioFormat.Bitrate {
				audioFormat = f
			}
		}

		ytstream, _, err := YtCl.GetStreamContext(Ctx, vinfo, &audioFormat)
		if err != nil {
			log("GetStreamContext: %v", err)
			break
		}
		defer ytstream.Close()

		audioBuf = bytes.NewBuffer(nil)
		_, err = io.Copy(audioBuf, ytstream)
		if err != nil {
			break
		}

		log(
			"Downloaded audio size:%dmb bitrate:%dkbps duration:%ds",
			audioBuf.Len()/1000/1000,
			audioFormat.Bitrate/1024,
			int64(vinfo.Duration.Seconds()),
		)
		if audioBuf.Len()/1000/1000 < 1 {
			log("Downloaded audio less than one megabyte, something is wrong, aborting.")
			break
		}

		audioSrcFile := fmt.Sprintf("%s.m4a", audioName)
		err = ioutil.WriteFile(audioSrcFile, audioBuf.Bytes(), 0400)
		if err != nil {
			log("WriteFile %s: %v", audioSrcFile, err)
			break
		}

		audioFile := fmt.Sprintf("%s.%s.m4a", audioName, TgAudioBitrate)
		err = exec.Command(
			FfmpegPath, "-v", "panic",
			"-i", audioSrcFile,
			"-b:a", TgAudioBitrate, audioFile,
		).Run()
		if err != nil {
			log("ffmpeg: %v", err)
			break
		}

		err = os.Remove(audioSrcFile)
		if err != nil {
			log("Remove %s: %v", audioSrcFile, err)
		}

		abb, err := ioutil.ReadFile(audioFile)
		if err != nil {
			log("ReadFile %s: %v", audioFile, err)
			break
		}
		audioBuf = bytes.NewBuffer(abb)

		log(
			"Final converted audio size:%dmb bitrate:%sbps",
			audioBuf.Len()/1000/1000, TgAudioBitrate,
		)

		err = os.Remove(audioFile)
		if err != nil {
			log("Remove %s: %v", audioFile, err)
		}

		tgcover, err := tgsendPhotoFile(audioName, coverBuf, title)
		if err != nil {
			log("tgsendPhotoFile: %v", err)
			break
		}
		if tgcover.FileId == "" {
			log("tgsendPhotoFile: file_id empty")
			break
		}

		tgaudio, err := tgsendAudioFile(
			TgPerformer,
			title,
			audioName,
			audioBuf,
			thumbBuf,
			vinfo.Duration,
		)
		if err != nil {
			log("tgsendAudioFile: %v", err)
			break
		}
		if tgaudio.FileId == "" {
			log("tgsendAudioFile: file_id empty")
			break
		}

		_, err = tgsendPhoto(tgcover.FileId, title)
		if err != nil {
			log("tgsendPhoto: %v", err)
			break
		}

		_, err = tgsendAudio(tgaudio.FileId)
		if err != nil {
			log("tgsendAudio: %v", err)
			break
		}

		_, err = tgsendMessage(description)
		if err != nil {
			log("tgsendMessage: %v", err)
			break
		}

		err = Setenv("YtLast", audioName)
		if err != nil {
			log("Setenv YtLast: %v", err)
			break
		}

		log("#%d uploaded", vidnum+1)
	}

	return
}

func tgsendAudioFile(performer, title string, fileName string, audioBuf, thumbBuf *bytes.Buffer, duration time.Duration) (audio *TgAudio, err error) {
	var mpartBuf bytes.Buffer
	mpart := multipart.NewWriter(&mpartBuf)
	var formWr io.Writer

	// chat_id
	formWr, err = mpart.CreateFormField("chat_id")
	if err != nil {
		return nil, fmt.Errorf("CreateFormField(`chat_id`): %v", err)
	}
	_, err = formWr.Write([]byte(TgChatId))
	if err != nil {
		return nil, fmt.Errorf("Write(chat_id): %v", err)
	}

	// performer
	formWr, err = mpart.CreateFormField("performer")
	if err != nil {
		return nil, fmt.Errorf("CreateFormField(`performer`): %v", err)
	}
	_, err = formWr.Write([]byte(performer))
	if err != nil {
		return nil, fmt.Errorf("Write(performer): %v", err)
	}

	// title
	formWr, err = mpart.CreateFormField("title")
	if err != nil {
		return nil, fmt.Errorf("CreateFormField(`title`): %v", err)
	}
	_, err = formWr.Write([]byte(title))
	if err != nil {
		return nil, fmt.Errorf("Write(title): %v", err)
	}

	// audio
	formWr, err = mpart.CreateFormFile("audio", fileName)
	if err != nil {
		return nil, fmt.Errorf("CreateFormFile('audio'): %v", err)
	}
	_, err = io.Copy(formWr, audioBuf)
	if err != nil {
		return nil, fmt.Errorf("Copy audio: %v", err)
	}

	// thumb
	formWr, err = mpart.CreateFormFile("thumb", fileName+".thumb")
	if err != nil {
		return nil, fmt.Errorf("CreateFormFile(`thumb`): %v", err)
	}
	_, err = io.Copy(formWr, thumbBuf)
	if err != nil {
		return nil, fmt.Errorf("Copy thumb: %v", err)
	}

	// duration
	formWr, err = mpart.CreateFormField("duration")
	if err != nil {
		return nil, fmt.Errorf("CreateFormField(`duration`): %v", err)
	}
	_, err = formWr.Write([]byte(strconv.Itoa(int(duration.Seconds()))))
	if err != nil {
		return nil, fmt.Errorf("Write(duration): %v", err)
	}

	err = mpart.Close()
	if err != nil {
		return nil, fmt.Errorf("multipartWriter.Close: %v", err)
	}

	resp, err := HttpClient.Post(
		fmt.Sprintf("https://api.telegram.org/bot%s/sendAudio", TgToken),
		mpart.FormDataContentType(),
		&mpartBuf,
	)
	if err != nil {
		return nil, fmt.Errorf("Post: %v", err)
	}
	defer resp.Body.Close()

	var tgresp TgResponse
	err = json.NewDecoder(resp.Body).Decode(&tgresp)
	if err != nil {
		return nil, fmt.Errorf("Decode: %v", err)
	}
	if !tgresp.Ok {
		return nil, fmt.Errorf("sendAudio: %s", tgresp.Description)
	}

	msg := tgresp.Result
	msg.Id = fmt.Sprintf("%d", msg.MessageId)

	audio = &msg.Audio

	if audio.FileId == "" {
		return nil, fmt.Errorf("sendAudio: Audio.FileId empty")
	}

	err = tgdeleteMessage(msg.MessageId)
	if err != nil {
		return nil, fmt.Errorf("tgdeleteMessage(%d): %v", msg.MessageId, err)
	}

	return audio, nil
}

func tgsendAudio(fileid string) (msg *TgMessage, err error) {
	sendAudio := map[string]interface{}{
		"chat_id": TgChatId,
		"audio":   fileid,
	}
	sendAudioJSON, err := json.Marshal(sendAudio)
	if err != nil {
		return nil, err
	}

	var tgresp TgResponse
	err = postJson(
		fmt.Sprintf("https://api.telegram.org/bot%s/sendAudio", TgToken),
		bytes.NewBuffer(sendAudioJSON),
		&tgresp,
	)
	if err != nil {
		return nil, err
	}

	if !tgresp.Ok {
		return nil, fmt.Errorf("sendAudio: %s", tgresp.Description)
	}

	msg = tgresp.Result
	msg.Id = fmt.Sprintf("%d", msg.MessageId)

	return msg, nil
}

func tgsendPhotoFile(fileName string, photoBuf *bytes.Buffer, caption string) (photo *TgPhotoSize, err error) {
	var mpartBuf bytes.Buffer
	mpart := multipart.NewWriter(&mpartBuf)
	var formWr io.Writer

	// chat_id
	formWr, err = mpart.CreateFormField("chat_id")
	if err != nil {
		return nil, fmt.Errorf("CreateFormField(`chat_id`): %v", err)
	}
	_, err = formWr.Write([]byte(TgChatId))
	if err != nil {
		return nil, fmt.Errorf("Write(chat_id): %v", err)
	}

	// photo
	formWr, err = mpart.CreateFormFile("photo", fileName+".cover")
	if err != nil {
		return nil, fmt.Errorf("CreateFormFile(`photo`): %v", err)
	}
	_, err = io.Copy(formWr, photoBuf)
	if err != nil {
		return nil, fmt.Errorf("Copy photo: %v", err)
	}

	err = mpart.Close()
	if err != nil {
		return nil, fmt.Errorf("multipartWriter.Close: %v", err)
	}

	resp, err := HttpClient.Post(
		fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", TgToken),
		mpart.FormDataContentType(),
		&mpartBuf,
	)
	if err != nil {
		return nil, fmt.Errorf("Post: %v", err)
	}
	defer resp.Body.Close()

	var tgresp TgResponse
	err = json.NewDecoder(resp.Body).Decode(&tgresp)
	if err != nil {
		return nil, fmt.Errorf("Decode: %v", err)
	}
	if !tgresp.Ok {
		return nil, fmt.Errorf("sendPhoto: %s", tgresp.Description)
	}

	msg := tgresp.Result
	msg.Id = fmt.Sprintf("%d", msg.MessageId)

	if len(msg.Photo) == 0 {
		return nil, fmt.Errorf("sendPhoto: Photo empty")
	}

	photo = &TgPhotoSize{}
	for _, p := range msg.Photo {
		if p.Width > photo.Width {
			photo = &p
		}
	}

	if photo.FileId == "" {
		return nil, fmt.Errorf("sendPhoto: Photo.FileId empty")
	}

	err = tgdeleteMessage(msg.MessageId)
	if err != nil {
		return nil, fmt.Errorf("tgdeleteMessage(%d): %v", msg.MessageId, err)
	}

	return photo, nil
}

func tgsendPhoto(fileid, caption string) (msg *TgMessage, err error) {
	caption = fmt.Sprintf("<u><b>%s</b></u>", caption)
	sendPhoto := map[string]interface{}{
		"chat_id":    TgChatId,
		"photo":      fileid,
		"caption":    caption,
		"parse_mode": "HTML",
	}
	sendPhotoJSON, err := json.Marshal(sendPhoto)
	if err != nil {
		return nil, err
	}

	var tgresp TgResponse
	err = postJson(
		fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", TgToken),
		bytes.NewBuffer(sendPhotoJSON),
		&tgresp,
	)
	if err != nil {
		return nil, err
	}

	if !tgresp.Ok {
		return nil, fmt.Errorf("sendPhoto: %s", tgresp.Description)
	}

	msg = tgresp.Result
	msg.Id = fmt.Sprintf("%d", msg.MessageId)

	return msg, nil
}

func tgsendMessage(message string) (msg *TgMessage, err error) {
	sendMessage := map[string]interface{}{
		"chat_id": TgChatId,
		"text":    message,

		"disable_web_page_preview": true,
	}
	sendMessageJSON, err := json.Marshal(sendMessage)
	if err != nil {
		return nil, err
	}

	var tgresp TgResponse
	err = postJson(
		fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", TgToken),
		bytes.NewBuffer(sendMessageJSON),
		&tgresp,
	)
	if err != nil {
		return nil, err
	}

	if !tgresp.Ok {
		return nil, fmt.Errorf("sendMessage: %s", tgresp.Description)
	}

	msg = tgresp.Result
	msg.Id = fmt.Sprintf("%d", msg.MessageId)

	return msg, nil
}

func tgdeleteMessage(messageid int64) error {
	deleteMessage := map[string]interface{}{
		"chat_id":    TgChatId,
		"message_id": messageid,
	}
	deleteMessageJSON, err := json.Marshal(deleteMessage)
	if err != nil {
		return err
	}

	var tgresp TgResponseShort
	err = postJson(
		fmt.Sprintf("https://api.telegram.org/bot%s/deleteMessage", TgToken),
		bytes.NewBuffer(deleteMessageJSON),
		&tgresp,
	)
	if err != nil {
		return fmt.Errorf("postJson: %v", err)
	}

	if !tgresp.Ok {
		return fmt.Errorf("deleteMessage: %s", tgresp.Description)
	}

	return nil
}
