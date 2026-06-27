$login = Invoke-WebRequest -Uri "http://127.0.0.1:9090/api/login" -Method POST -ContentType "application/json" -Body '{"username":"admin","password":"admin"}' -UseBasicParsing -SessionVariable sess
Write-Host "Login:" $login.Content

$sub = Invoke-WebRequest -Uri "http://127.0.0.1:9090/api/subscription/token" -UseBasicParsing -WebSession $sess
Write-Host "Token:" $sub.Content
