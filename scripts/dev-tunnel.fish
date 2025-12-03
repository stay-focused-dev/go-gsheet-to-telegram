#!/usr/bin/env fish

set COLOR_GREEN (set_color green)
set COLOR_RED (set_color red)
set COLOR_YELLOW (set_color yellow)
set COLOR_RESET (set_color normal)

function log_info
    printf "%s[INFO]%s %s\n" "$COLOR_GREEN" "$COLOR_RESET" "$argv"
end

function log_error
    printf "%s[ERROR]%s %s\n" "$COLOR_RED" "$COLOR_RESET" "$argv"
end

function log_warn
    printf "%s[WARN]%s %s\n" "$COLOR_YELLOW" "$COLOR_RESET" "$argv"
end

function check_local_port
    if lsof -Pi :$DRIVE_WEBHOOK_PORT -sTCP:LISTEN -t >/dev/null 2>&1
        log_info "Port $DRIVE_WEBHOOK_PORT is already in use (your app is running)"
        return 0
    else
        log_warn "Port $DRIVE_WEBHOOK_PORT is not in use. Make sure your Go app is running!"
        return 1
    end
end

function cleanup
    log_info "Shutting down tunnel..."
    exit 0
end

trap cleanup SIGINT SIGTERM

log_info "Starting SSH reverse tunnel..."
log_info "Local port: $DRIVE_WEBHOOK_PORT"
log_info "Remote host: $DRIVE_WEBHOOK_HOST"
log_info "Remote port: $DRIVE_WEBHOOK_REMOTE_PORT"
log_info "Press Ctrl+C to stop"
echo ""

while true
    log_info "Establishing SSH tunnel..."
    
    ssh -R $DRIVE_WEBHOOK_REMOTE_PORT:localhost:$DRIVE_WEBHOOK_PORT \
        -o ServerAliveInterval=60 \
        -o ServerAliveCountMax=3 \
        -o ExitOnForwardFailure=yes \
        -o StrictHostKeyChecking=no \
        $DRIVE_WEBHOOK_HOST -N
    
    set exit_code $status
    
    if test $exit_code -eq 0
        log_info "Tunnel closed gracefully"
        break
    else
        log_error "Tunnel disconnected (exit code: $exit_code)"
        log_info "Reconnecting in 5 seconds..."
        sleep 5
    end
end