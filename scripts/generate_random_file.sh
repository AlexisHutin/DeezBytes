#!/bin/bash

DIR=${1:-.}

FILENAME="file_$(date +%s%N).bin"

SIZE=$(( (RANDOM % 10240 + 1) * 1024 ))  # 1K to 10M

echo "Generating $FILENAME, size: $SIZE bytes, in $DIR"

head -c "$SIZE" /dev/urandom > "$DIR/$FILENAME"

ls -lh "$DIR/$FILENAME"
