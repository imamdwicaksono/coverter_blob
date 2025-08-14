#!/bin/bash

TIMESTAMP=$(date +%Y%m%d_%H%M%S)
PID=$$
LOG_FILE="download_logs/download_output_${TIMESTAMP}_${PID}.log"

nohup ./run.sh > "$LOG_FILE" 2>&1 &
echo "Download berjalan di background (PID $!). Log: $LOG_FILE"