FROM public.ecr.aws/docker/library/python:latest

RUN pip3 install boto3 --progress-bar off

WORKDIR /

COPY validator.py .

CMD ["python", "validator.py"]
