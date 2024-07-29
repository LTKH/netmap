NAMESPACE=default

all: 
	${MAKE} docker

docker:
	printf "\033[32m------------------ docker build\033[0m\n"
	docker build -t netserver:latest .
