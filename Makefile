NAMESPACE=default

all: 
	${MAKE} docker

docker:
	printf "\033[32m------------------ docker build\033[0m\n"
	DOCKER_BUILDKIT=1 docker build -t netserver:latest .
