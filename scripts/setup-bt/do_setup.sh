#!/bin/bash

# Install srs-cloud, for example:
#   bash /www/server/panel/plugin/srs_cloud/setup.sh --r0 /tmp/srs_cloud_install.r0 --nginx /www/server/nginx/logs/nginx.pid --www /www/wwwroot --site srs.cloud.local
# If ok, we will create systemctl service at:
#   /usr/lib/systemd/system/srs-cloud.service

HELP=no
R0_FILE=
NGINX_PID=
WWW_HOME=
SITE_NAME=
install_path=/www/server/panel/plugin/srs_cloud
SRS_HOME=/usr/local/srs-cloud
DATA_HOME=/data

HELP=no
VERBOSE=no
LANGUAGE=zh
REGISTRY=auto
REGION=auto
IMAGE=ossrs/srs-cloud:1

# Allow use .env to override the default values.
if [[ -f ${install_path}/.env ]]; then source ${install_path}/.env; fi

while [[ "$#" -gt 0 ]]; do
    case $1 in
        -h|--help) HELP=yes; shift ;;
        --r0) R0_FILE=$2; shift 2 ;;
        --nginx) NGINX_PID=$2; shift 2 ;;
        --www) WWW_HOME=$2; shift 2 ;;
        --site) SITE_NAME=$2; shift 2 ;;
        *) echo "Unknown parameter passed: $1"; exit 1 ;;
    esac
done

if [[ "$HELP" == yes ]]; then
    echo "Usage: $0 [OPTIONS]"
    echo "Options:"
    echo "  -h, --help    Show this help message and exit"
    echo "  --r0          The install error file. Default: ${R0_FILE}"
    echo "  --nginx       The NGINX pid file. Default: ${NGINX_PID}"
    echo "  --www         The www home directory. Default: ${WWW_HOME}"
    echo "  --site        The website name. Default: ${SITE_NAME}"
    exit 0
fi

# Guess the registry automatically by language.
if [[ $REGISTRY == auto ]]; then
    REGISTRY=$([[ $LANGUAGE == zh ]] && echo registry.cn-hangzhou.aliyuncs.com || echo docker.io)
    REGION=$([[ $LANGUAGE == zh ]] && echo ap-beijing || echo ap-singapore)
    IMAGE_URL=$([[ $REGISTRY == docker.io ]] && echo ${IMAGE} || echo ${REGISTRY}/${IMAGE})
fi

if [[ -z $NGINX_PID ]]; then echo "The nginx is empty"; exit 1; fi
if [[ ! -f $NGINX_PID ]]; then echo "No nginx pid at $NGINX_PID"; exit 1; fi
if [[ ! -f /etc/init.d/nginx ]]; then echo "No nginx file at /etc/init.d/nginx"; exit 1; fi
if [[ -z $WWW_HOME ]]; then echo "No www home"; exit 1; fi
if [[ -z $SITE_NAME ]]; then echo "No site name"; exit 1; fi

echo "Setup SRS at install_path=$install_path, SRS_HOME=$SRS_HOME, DATA_HOME=$DATA_HOME, r0=$R0_FILE, NGINX_PID=$NGINX_PID, WWW_HOME=$WWW_HOME, SITE_NAME=$SITE_NAME"
echo "ENV LANGUAGE=$LANGUAGE, IMAGE=$IMAGE, REGISTRY=$REGISTRY, REGION=$REGION, IMAGE_URL=$IMAGE_URL"

source do_os.sh
if [[ $? -ne 0 ]]; then echo "Setup OS failed"; exit 1; fi

# Setup the environment variables.
echo "Start to setup .env"
if [[ -f ${DATA_HOME}/config/.env && -s ${DATA_HOME}/config/.env ]]; then
  echo "The .env already exists, skip"
else
  mkdir -p ${DATA_HOME}/config &&
  cat << END > ${DATA_HOME}/config/.env
CLOUD=BT
REGION=${REGION}
REACT_APP_LOCALE=${LANGUAGE}
IMAGE=${IMAGE_URL}
# Please use BT to configure the domain and HTTPS.
SRS_HTTPS=off
NGINX_PID=$NGINX_PID
END
  if [[ $? -ne 0 ]]; then echo "Setup .env failed"; exit 1; fi
fi

echo "Start to update bootstrap"
sed -i "s|^DATA_HOME=.*|DATA_HOME=${DATA_HOME}|g" ${SRS_HOME}/mgmt/bootstrap &&
sed -i "s|^IMAGE=.*|IMAGE=${IMAGE_URL}|g" ${SRS_HOME}/mgmt/bootstrap &&
if [[ $? -ne 0 ]]; then echo "Update bootstrap failed"; exit 1; fi
echo "Update bootstrap ok"

# Update the docker images.
echo "Cache docker image ${IMAGE}" &&
REPO=$(echo $IMAGE |cut -d: -f1) && TAG=$(echo $IMAGE |cut -d: -f2)
if [[ $(docker images |grep $REPO |grep -q $TAG || echo no) == no ]]; then
  docker pull ${IMAGE}
fi
if [[ $? -ne 0 ]]; then echo "Cache docker images failed"; exit 1; fi

# If install ok, the directory should exists.
if [[ ! -d ${SRS_HOME} || ! -d ${SRS_HOME}/mgmt ]]; then
  echo "Install srs-cloud failed"; exit 1;
fi

# Link the www root to container.
#WWW_FILES=$(ls ${SRS_HOME}/mgmt/containers/www)
#for file in $WWW_FILES; do
#  rm -rf $WWW_HOME/$SITE_NAME/$file &&
#  ln -sf ${SRS_HOME}/mgmt/containers/www/$file $WWW_HOME/$SITE_NAME/$file
#done
#if [[ $? -ne 0 ]]; then echo "Link www root failed"; exit 1; fi

# Create init.d script.
rm -f /etc/init.d/srs_cloud &&
cp $install_path/init.d.sh /etc/init.d/srs_cloud &&
chmod +x /etc/init.d/srs_cloud
if [[ $? -ne 0 ]]; then echo "Setup init.d script failed"; exit 1; fi

# Create srs-cloud service, and the credential file.
# Remark: Never start the service, because the IP will change for new machine created.
cd ${SRS_HOME} &&
cp -f usr/lib/systemd/system/srs-cloud.service /usr/lib/systemd/system/srs-cloud.service &&
systemctl daemon-reload && systemctl enable srs-cloud
if [[ $? -ne 0 ]]; then echo "Install srs-cloud failed"; exit 1; fi

/etc/init.d/srs_cloud restart srs-cloud
if [[ $? -ne 0 ]]; then echo "Start srs-cloud failed"; exit 1; fi

