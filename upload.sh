#!/bin/bash

TIMESTAMP=$(date +%Y%m%d_%H%M%S)
PID=$$
LOG_FILE="upload_logs/upload_output_${TIMESTAMP}_${PID}.log"

nohup ./run.sh --only-upload-sp > "$LOG_FILE" 2>&1 &
echo "Upload berjalan di background (PID $!). Log: $LOG_FILE"