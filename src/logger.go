/*
Copyright 2022 The Matrix.org Foundation C.I.C.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"

	"github.com/sirupsen/logrus"
)

type CustomTextFormatter struct {
	logrus.TextFormatter
	logTime bool
}

func (f *CustomTextFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	// TODO: Use colors and make it pretty
	level := entry.Level
	timestamp := entry.Time.Format("2006-01-02 15:04:05")
	confID := entry.Data["conf_id"]
	userID := entry.Data["user_id"]

	logLine := fmt.Sprintf("%s|", level)

	if f.logTime {
		logLine += fmt.Sprintf("%s|", timestamp)
	}

	if confID != nil {
		logLine += fmt.Sprintf("%v|", confID)
	}

	if userID != nil {
		logLine += fmt.Sprintf("%v|", userID)
	}

	logLine += fmt.Sprintf(" %v ", entry.Message)

	fields := ""

	for key, value := range entry.Data {
		if key != "conf_id" && key != "user_id" {
			fields += fmt.Sprintf("%v=%v ", key, value)
		}
	}

	if fields != "" {
		logLine += fmt.Sprintf("| %s", fields)
	}

	logLine += "\n"

	return []byte(logLine), nil
}
