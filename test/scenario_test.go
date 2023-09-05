package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ossrs/go-oryx-lib/errors"
	"github.com/ossrs/go-oryx-lib/logger"
)

func TestApi_PublishVLivePlayFlv(t *testing.T) {
	ctx, cancel := context.WithTimeout(logger.WithContext(context.Background()), time.Duration(*srsTimeout)*time.Millisecond)
	defer cancel()

	if *noMediaTest {
		return
	}

	var r0, r1, r2, r3, r4, r5 error
	defer func(ctx context.Context) {
		if err := filterTestError(ctx.Err(), r0, r1, r2, r3, r4, r5); err != nil {
			t.Errorf("Fail for err %+v", err)
		} else {
			logger.Tf(ctx, "test done")
		}
	}(ctx)

	var pubSecret string
	if err := apiRequest(ctx, "/terraform/v1/hooks/srs/secret/query", nil, &struct {
		Publish *string `json:"publish"`
	}{
		Publish: &pubSecret,
	}); err != nil {
		r0 = err
		return
	}

	// Copy virtual live source file to /data/upload and platform/containers/data/upload
	destDirs := []string{
		"/data/upload/",
		"platform/containers/data/upload",
		"../platform/containers/data/upload",
	}
	if err := copyToDest(ctx, *srsInputFile, destDirs...); err != nil {
		r0 = errors.Wrapf(err, "copy %v to %v", *srsInputFile, destDirs)
		return
	}

	// Get first matched source file.
	sourceFile := getExistsFile(ctx, filepath.Base(*srsInputFile), destDirs...)
	if sourceFile == "" {
		r0 = errors.Errorf("no source file found")
		return
	}

	// If not absolution path, always use short path in upload.
	if !strings.HasPrefix(sourceFile, "/data/upload/") {
		sourceFile = "upload/" + path.Base(sourceFile)
	}

	// Use the file as uploaded file.
	res := struct {
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		Target string `json:"target"`
		UUID   string `json:"uuid"`
	}{}
	if err := apiRequest(ctx, "/terraform/v1/ffmpeg/vlive/server?file="+sourceFile, nil, &res); err != nil {
		r0 = errors.Wrapf(err, "request ffmpeg vlive server failed")
		return
	}

	// Use the file as source file.
	codec := struct {
		UUID  string `json:"uuid"`
		Audio struct {
			CodecName  string `json:"codec_name"`
			Channels   int    `json:"channels"`
			SampleRate string `json:"sample_rate"`
		} `json:"audio"`
		Video struct {
			CodecName string `json:"codec_name"`
			Profile   string `json:"profile"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"video"`
	}{}
	if err := apiRequest(ctx, "/terraform/v1/ffmpeg/vlive/source", &struct {
		Platform string        `json:"platform"`
		Files    []interface{} `json:"files"`
	}{
		Platform: "bilibili",
		Files:    []interface{}{res},
	}, &struct {
		Files []interface{} `json:"files"`
	}{
		Files: []interface{}{&codec},
	}); err != nil {
		r0 = errors.Wrapf(err, "request ffmpeg vlive source failed")
		return
	}

	if err := func() error {
		if codec.UUID != res.UUID {
			return errors.Errorf("invalid codec uuid=%v, %v", codec.UUID, res.UUID)
		}
		if codec.Audio.CodecName != "aac" || codec.Audio.Channels != 2 || codec.Audio.SampleRate != "44100" {
			return errors.Errorf("invalid codec audio=%v", codec.Audio)
		}
		if codec.Video.CodecName != "h264" || codec.Video.Profile != "High" || codec.Video.Width != 768 || codec.Video.Height != 320 {
			return errors.Errorf("invalid codec video=%v", codec.Video)
		}
		return nil
	}(); err != nil {
		r0 = errors.Wrapf(err, "request ffmpeg vlive source failed")
		return
	}

	// Start virtual live streaming.
	conf := make(map[string]interface{})
	if err := apiRequest(ctx, "/terraform/v1/ffmpeg/vlive/secret", nil, &conf); err != nil {
		r0 = errors.Wrapf(err, "request ffmpeg vlive secret failed")
		return
	}

	bilibili, ok := conf["bilibili"].(map[string]interface{})
	if !ok || bilibili == nil {
		r0 = errors.Errorf("invalid bilibili secret")
		return
	}
	bilibili["action"] = "update"

	// Restore the state of enabled.
	backup := make(map[string]interface{})
	for k, v := range bilibili {
		backup[k] = v
	}
	defer func() {
		logger.Tf(ctx, "restore config %v", backup)

		// The ctx has already been cancelled by test case, which will cause the request failed.
		ctx := context.Background()
		apiRequest(ctx, "/terraform/v1/ffmpeg/vlive/secret", backup, nil)
	}()

	streamID := fmt.Sprintf("stream-%v-%v", os.Getpid(), rand.Int())
	bilibili["secret"] = fmt.Sprintf("%v?secret=%v", streamID, pubSecret)
	bilibili["server"] = "rtmp://localhost/live/"
	bilibili["enabled"] = true
	if err := apiRequest(ctx, "/terraform/v1/ffmpeg/vlive/secret", &bilibili, nil); err != nil {
		r0 = errors.Wrapf(err, "request ffmpeg vlive secret failed")
		return
	}

	var wg sync.WaitGroup
	defer wg.Wait()

	// Start FFprobe to detect and verify stream.
	duration := time.Duration(*srsFFprobeDuration) * time.Millisecond
	ffprobe := NewFFprobe(func(v *ffprobeClient) {
		v.dvrFile = fmt.Sprintf("srs-ffprobe-%v.flv", streamID)
		v.streamURL = fmt.Sprintf("%v/live/%v.flv", *endpointHTTP, streamID)
		v.duration, v.timeout = duration, time.Duration(*srsFFprobeTimeout)*time.Millisecond
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		r2 = ffprobe.Run(ctx, cancel)
	}()

	// Fast quit for probe done.
	select {
	case <-ctx.Done():
	case <-ffprobe.ProbeDoneCtx().Done():
		cancel()
	}

	str, m := ffprobe.Result()
	if len(m.Streams) != 2 {
		r3 = errors.Errorf("invalid streams=%v, %v, %v", len(m.Streams), m.String(), str)
	}

	if ts := 90; m.Format.ProbeScore < ts {
		r4 = errors.Errorf("low score=%v < %v, %v, %v", m.Format.ProbeScore, ts, m.String(), str)
	}
	if dv := m.Duration(); dv < duration/2 {
		r5 = errors.Errorf("short duration=%v < %v, %v, %v", dv, duration, m.String(), str)
	}
}

func TestApi_PublishRtmpRecordMp4(t *testing.T) {
	ctx, cancel := context.WithTimeout(logger.WithContext(context.Background()), time.Duration(*srsLongTimeout)*time.Millisecond)
	defer cancel()

	if *noMediaTest {
		return
	}

	var r0, r1, r2, r3, r4, r5 error
	defer func(ctx context.Context) {
		if err := filterTestError(ctx.Err(), r0, r1, r2, r3, r4, r5); err != nil {
			t.Errorf("Fail for err %+v", err)
		} else {
			logger.Tf(ctx, "test done")
		}
	}(ctx)

	var pubSecret string
	if err := apiRequest(ctx, "/terraform/v1/hooks/srs/secret/query", nil, &struct {
		Publish *string `json:"publish"`
	}{
		Publish: &pubSecret,
	}); err != nil {
		r0 = err
		return
	}

	// Query the old config.
	backup := make(map[string]interface{})
	if err := apiRequest(ctx, "/terraform/v1/hooks/record/query", nil, &backup); err != nil {
		r0 = errors.Wrapf(err, "request record query failed")
		return
	}
	defer func() {
		logger.Tf(ctx, "restore config %v", backup)

		// The ctx has already been cancelled by test case, which will cause the request failed.
		ctx := context.Background()
		apiRequest(ctx, "/terraform/v1/hooks/record/apply", backup, nil)
	}()

	// Enable the record worker.
	if err := apiRequest(ctx, "/terraform/v1/hooks/record/apply", &struct {
		All bool `json:"all"`
	}{true}, nil); err != nil {
		r0 = errors.Wrapf(err, "request record apply failed")
		return
	}

	var wg sync.WaitGroup
	defer wg.Wait()

	// Start FFmpeg to publish stream.
	streamID := fmt.Sprintf("stream-%v-%v", os.Getpid(), rand.Int())
	streamURL := fmt.Sprintf("%v/live/%v?secret=%v", *endpointRTMP, streamID, pubSecret)
	ffmpeg := NewFFmpeg(func(v *ffmpegClient) {
		v.args = []string{
			"-re", "-stream_loop", "-1", "-i", *srsInputFile, "-c", "copy",
			"-f", "flv", streamURL,
		}
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		r1 = ffmpeg.Run(ctx, cancel)
	}()

	// Wait for record to save file.
	select {
	case <-ctx.Done():
	case <-time.After(25 * time.Second):
	}

	// Stop record worker.
	if err := apiRequest(ctx, "/terraform/v1/hooks/record/apply", &struct {
		All bool `json:"all"`
	}{false}, nil); err != nil {
		r0 = errors.Wrapf(err, "request record apply failed")
		return
	}
	logger.Tf(ctx, "stop record worker done")

	// Query the record file.
	type RecordFile struct {
		Stream   string  `json:"stream"`
		UUID     string  `json:"uuid"`
		Duration float64 `json:"duration"`
		Progress bool    `json:"progress"`
	}
	var recordFile *RecordFile
	defer func() {
		if recordFile == nil || recordFile.UUID == "" {
			return
		}
		logger.Tf(ctx, "remove record file %v", recordFile)

		// The ctx has already been cancelled by test case, which will cause the request failed.
		ctx := context.Background()
		apiRequest(ctx, "/terraform/v1/hooks/record/remove", &struct {
			UUID string `json:"uuid"`
		}{recordFile.UUID}, nil)
	}()
	defer cancel()

	for i := 0; i < 60; i++ {
		files := []RecordFile{}
		if err := apiRequest(ctx, "/terraform/v1/hooks/record/files", nil, &files); err != nil {
			r0 = errors.Wrapf(err, "request record files failed")
			return
		}

		for _, file := range files {
			if file.Stream == streamID {
				recordFile = &file
				break
			}
		}

		if recordFile == nil || recordFile.Progress {
			select {
			case <-ctx.Done():
				r0 = errors.Wrapf(ctx.Err(), "record file not found")
				return
			case <-time.After(1 * time.Second):
				continue
			}
		}
		break
	}

	if recordFile == nil {
		r0 = errors.Errorf("record file not found")
		return
	}
	if recordFile.Progress {
		r0 = errors.Errorf("record file is progress, %v", recordFile)
		return
	}
	if recordFile.Duration < 10 {
		r0 = errors.Errorf("record file duration too short, %v", recordFile)
		return
	}

	time.Sleep(3 * time.Second)
	logger.Tf(ctx, "record ok, file is %v", recordFile)
	cancel()
}
